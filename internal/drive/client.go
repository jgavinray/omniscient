package drive

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// Transcript represents a Google Drive document that has been downloaded
// and exported as plain text.
type Transcript struct {
	ID         string
	Name       string
	ModifiedAt time.Time
	Content    string
}

// Client provides authenticated access to the Google Drive API.
type Client struct {
	service *drive.Service
}

// NewClient creates an authenticated Drive client. It loads the OAuth config
// from credentialsPath and the saved token from tokenPath, then constructs a
// Drive service with an auto-refreshing token source.
//
// The token must already exist — run the `auth` command first to perform the
// interactive browser consent flow.
func NewClient(ctx context.Context, credentialsPath, tokenPath string) (*Client, error) {
	config, err := loadOAuthConfig(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("loading OAuth config: %w", err)
	}

	token, err := loadToken(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("loading token: %w", err)
	}
	if token == nil {
		return nil, fmt.Errorf("no token found at %s — run the 'auth' command first to authorize", tokenPath)
	}

	tokenSource := getTokenSource(config, tokenPath, token)
	httpClient := oauth2.NewClient(ctx, tokenSource)

	service, err := drive.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("creating Drive service: %w", err)
	}

	slog.Info("Google Drive client initialized")

	return &Client{service: service}, nil
}

// GetRecentTranscripts fetches transcript documents from folderID that have
// been modified within the given duration. Each document is exported as
// plain text. If a single file export fails, the error is logged and that
// file is skipped — remaining files are still processed.
func (c *Client) GetRecentTranscripts(ctx context.Context, folderID string, since time.Duration) ([]*Transcript, error) {
	cutoff := time.Now().UTC().Add(-since)
	cutoffStr := cutoff.Format(time.RFC3339)

	query := fmt.Sprintf(
		"mimeType='application/vnd.google-apps.document' and modifiedTime > '%s' and '%s' in parents and trashed = false",
		cutoffStr, folderID,
	)

	slog.Info("querying Google Drive for recent transcripts",
		"folder_id", folderID,
		"since", cutoffStr,
	)

	var transcripts []*Transcript
	pageToken := ""

	for {
		call := c.service.Files.List().
			Context(ctx).
			Q(query).
			Fields("nextPageToken, files(id, name, modifiedTime)").
			OrderBy("modifiedTime desc").
			PageSize(100)

		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		result, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("listing files from Drive: %w", err)
		}

		for _, file := range result.Files {
			modifiedAt, err := time.Parse(time.RFC3339, file.ModifiedTime)
			if err != nil {
				slog.Warn("could not parse modifiedTime, skipping file",
					"file_id", file.Id,
					"file_name", file.Name,
					"modified_time", file.ModifiedTime,
					"error", err,
				)
				continue
			}

			content, err := c.exportFileAsText(ctx, file.Id)
			if err != nil {
				slog.Warn("failed to export file as text, skipping",
					"file_id", file.Id,
					"file_name", file.Name,
					"error", err,
				)
				continue
			}

			transcripts = append(transcripts, &Transcript{
				ID:         file.Id,
				Name:       file.Name,
				ModifiedAt: modifiedAt,
				Content:    content,
			})

			slog.Info("fetched transcript",
				"file_id", file.Id,
				"file_name", file.Name,
				"modified_at", modifiedAt,
			)
		}

		pageToken = result.NextPageToken
		if pageToken == "" {
			break
		}
	}

	slog.Info("completed transcript fetch",
		"total", len(transcripts),
		"folder_id", folderID,
	)

	return transcripts, nil
}

// exportFileAsText exports a Google Docs file as plain text.
func (c *Client) exportFileAsText(ctx context.Context, fileID string) (string, error) {
	resp, err := c.service.Files.Export(fileID, "text/plain").Context(ctx).Download()
	if err != nil {
		return "", fmt.Errorf("exporting file %s as text/plain: %w", fileID, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading exported content for file %s: %w", fileID, err)
	}

	content := strings.TrimSpace(string(data))
	return content, nil
}
