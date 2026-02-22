package confluence

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jgavinray/omniscient/internal/models"
	"github.com/jgavinray/omniscient/internal/retry"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// Client is a Confluence REST API client for publishing meeting transcript pages.
type Client struct {
	baseURL    string
	email      string
	apiToken   string
	httpClient *http.Client
}

// NewClient creates a new Confluence API client.
// It trims any trailing slash from baseURL and configures an HTTP client with a 30-second timeout.
func NewClient(baseURL, email, apiToken string) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		email:    email,
		apiToken: apiToken,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// markdownToHTML converts markdown text to HTML using goldmark with GFM table extension.
func markdownToHTML(markdown string) (string, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.Table),
	)

	var buf bytes.Buffer
	if err := md.Convert([]byte(markdown), &buf); err != nil {
		return "", fmt.Errorf("converting markdown to HTML: %w", err)
	}

	return buf.String(), nil
}

// extractDate returns the date from front-matter, or falls back to today's date.
func extractDate(fm map[string]any) string {
	if d, ok := fm["date"]; ok {
		if s, ok := d.(string); ok && s != "" {
			return s
		}
	}
	slog.Warn("no date in front-matter, using today")
	return time.Now().Format("2006-01-02")
}

// PublishMarkdown creates or updates a Confluence page from an ExtractionResult.
// It converts the markdown body to HTML and uses front-matter date for the title.
func (c *Client) PublishMarkdown(ctx context.Context, spaceKey, parentPageID string, result *models.ExtractionResult, transcriptName string) (string, error) {
	// Strip file extensions from transcript name for the page title.
	cleanName := transcriptName
	for _, ext := range []string{".gdoc", ".docx", ".doc", ".txt", ".pdf"} {
		cleanName = strings.TrimSuffix(cleanName, ext)
	}

	date := extractDate(result.FrontMatter)
	title := fmt.Sprintf("%s - %s", cleanName, date)

	htmlBody, err := markdownToHTML(result.Markdown)
	if err != nil {
		return "", fmt.Errorf("render HTML: %w", err)
	}

	// Check if the page already exists.
	existing, err := c.findPage(ctx, spaceKey, title)
	if err != nil {
		return "", fmt.Errorf("find existing page: %w", err)
	}

	var pageRes *pageResult
	if existing != nil {
		slog.Info("updating existing confluence page", "title", title, "id", existing.ID, "version", existing.Version.Number+1)
		pageRes, err = c.updatePage(ctx, existing.ID, title, htmlBody, existing.Version.Number+1)
		if err != nil {
			return "", fmt.Errorf("update page: %w", err)
		}
	} else {
		slog.Info("creating new confluence page", "title", title, "space", spaceKey)
		pageRes, err = c.createPage(ctx, spaceKey, parentPageID, title, htmlBody)
		if err != nil {
			return "", fmt.Errorf("create page: %w", err)
		}
	}

	pageURL := c.baseURL + pageRes.Links.WebUI
	slog.Info("published confluence page", "url", pageURL)
	return pageURL, nil
}

// findPage searches for an existing Confluence page by space key and title.
// Returns nil if no matching page is found.
func (c *Client) findPage(ctx context.Context, spaceKey, title string) (*pageResult, error) {
	path := fmt.Sprintf("/wiki/rest/api/content?spaceKey=%s&title=%s",
		url.QueryEscape(spaceKey),
		url.QueryEscape(title),
	)

	respBody, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}

	var searchResp searchResponse
	if err := json.Unmarshal(respBody, &searchResp); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}

	if len(searchResp.Results) == 0 {
		return nil, nil
	}

	return &searchResp.Results[0], nil
}

// createPage creates a new Confluence page in the given space.
func (c *Client) createPage(ctx context.Context, spaceKey, parentPageID, title, body string) (*pageResult, error) {
	payload := map[string]interface{}{
		"type":  "page",
		"title": title,
		"space": map[string]string{
			"key": spaceKey,
		},
		"body": map[string]interface{}{
			"storage": map[string]string{
				"value":          body,
				"representation": "storage",
			},
		},
	}

	if parentPageID != "" {
		payload["ancestors"] = []map[string]string{
			{"id": parentPageID},
		}
	}

	respBody, err := c.doRequest(ctx, http.MethodPost, "/wiki/rest/api/content", payload)
	if err != nil {
		return nil, err
	}

	var result pageResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode create response: %w", err)
	}

	return &result, nil
}

// updatePage updates an existing Confluence page with new content and version.
func (c *Client) updatePage(ctx context.Context, pageID, title, body string, version int) (*pageResult, error) {
	payload := map[string]interface{}{
		"type":  "page",
		"title": title,
		"version": map[string]int{
			"number": version,
		},
		"body": map[string]interface{}{
			"storage": map[string]string{
				"value":          body,
				"representation": "storage",
			},
		},
	}

	path := fmt.Sprintf("/wiki/rest/api/content/%s", pageID)

	respBody, err := c.doRequest(ctx, http.MethodPut, path, payload)
	if err != nil {
		return nil, err
	}

	var result pageResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode update response: %w", err)
	}

	return &result, nil
}

// doRequest executes an HTTP request against the Confluence API with retry logic
// for transient errors (HTTP 429 and 5xx). It retries up to 3 attempts with
// exponential backoff.
func (c *Client) doRequest(ctx context.Context, method, urlPath string, body interface{}) ([]byte, error) {
	fullURL := c.baseURL + urlPath

	const maxAttempts = 3

	var result []byte

	err := retry.Do(func() error {
		var reqBody io.Reader
		if body != nil {
			jsonData, err := json.Marshal(body)
			if err != nil {
				return fmt.Errorf("marshal request body: %w", err)
			}
			reqBody = bytes.NewReader(jsonData)
		}

		req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		// Set Basic Auth header: base64(email:api_token)
		credentials := base64.StdEncoding.EncodeToString([]byte(c.email + ":" + c.apiToken))
		req.Header.Set("Authorization", "Basic "+credentials)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("execute request: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("read response body: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return &retry.HTTPError{
				StatusCode: resp.StatusCode,
				Message:    string(respBody),
			}
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("confluence API returned status %d: %s",
				resp.StatusCode, string(respBody))
		}

		result = respBody
		return nil
	}, maxAttempts)

	if err != nil {
		return nil, err
	}

	return result, nil
}

// pageResult represents a Confluence page in API responses.
type pageResult struct {
	ID      string `json:"id"`
	Version struct {
		Number int `json:"number"`
	} `json:"version"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}

// searchResponse represents the Confluence content search API response.
type searchResponse struct {
	Results []pageResult `json:"results"`
}
