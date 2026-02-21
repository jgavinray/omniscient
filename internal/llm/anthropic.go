package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AnthropicExtractor implements Extractor for Anthropic's Messages API.
type AnthropicExtractor struct {
	apiKey     string
	model      string
	httpClient *http.Client
	baseURL    string // default "https://api.anthropic.com" — allow override for testing
}

// NewAnthropicExtractor creates an AnthropicExtractor configured with the given
// API key, model name, and HTTP timeout duration.
func NewAnthropicExtractor(apiKey, model string, timeout time.Duration) *AnthropicExtractor {
	return &AnthropicExtractor{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		baseURL: "https://api.anthropic.com",
	}
}

// anthropicRequest is the request body for the Anthropic Messages API.
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

// anthropicMessage represents a single message in the Anthropic conversation.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicResponse is the response body from the Anthropic Messages API.
type anthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// callAPI sends a prompt to the Anthropic API and returns the response text.
func (e *AnthropicExtractor) callAPI(prompt string, maxTokens int) (string, error) {
	reqBody := anthropicRequest{
		Model:     e.model,
		MaxTokens: maxTokens,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling anthropic request: %w", err)
	}

	var responseText string

	err = retryable(func() error {
		req, err := http.NewRequest("POST", e.baseURL+"/v1/messages", bytes.NewReader(jsonData))
		if err != nil {
			return fmt.Errorf("creating anthropic request: %w", err)
		}

		req.Header.Set("x-api-key", e.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("Content-Type", "application/json")

		resp, err := e.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("anthropic API request failed: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("reading anthropic response body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return &httpError{
				StatusCode: resp.StatusCode,
				Message:    string(body),
			}
		}

		var apiResp anthropicResponse
		if err := json.Unmarshal(body, &apiResp); err != nil {
			return fmt.Errorf("decoding anthropic response: %w", err)
		}

		if len(apiResp.Content) == 0 {
			return fmt.Errorf("empty content in anthropic response")
		}

		responseText = apiResp.Content[0].Text
		return nil
	}, 3)

	if err != nil {
		return "", err
	}

	return responseText, nil
}

// Classify sends a classification prompt to Anthropic and returns the meeting type key.
func (e *AnthropicExtractor) Classify(transcriptPreview string, templateKeys []string, classifyPrompt string) (string, error) {
	prompt := buildClassifyPrompt(classifyPrompt, transcriptPreview, templateKeys)

	text, err := e.callAPI(prompt, 32)
	if err != nil {
		return "", fmt.Errorf("anthropic classification failed: %w", err)
	}

	return strings.TrimSpace(text), nil
}

// Extract sends the transcript with the given extraction prompt to Anthropic
// and returns the raw cleaned text (markdown with YAML front-matter).
func (e *AnthropicExtractor) Extract(transcript string, extractionPrompt string) (string, error) {
	prompt := buildExtractionPrompt(extractionPrompt, transcript)

	text, err := e.callAPI(prompt, 4096)
	if err != nil {
		return "", fmt.Errorf("anthropic extraction failed: %w", err)
	}

	return cleanResponse(text), nil
}
