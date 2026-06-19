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

// OpenAIExtractor implements Extractor for OpenAI-compatible APIs
// (vLLM, Ollama, OpenAI, etc).
type OpenAIExtractor struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewOpenAIExtractor creates an OpenAIExtractor configured with the given base URL,
// API key, model name, and HTTP timeout duration. Trailing slashes are trimmed from
// the base URL.
func NewOpenAIExtractor(baseURL, apiKey, model string, timeout time.Duration) *OpenAIExtractor {
	return &OpenAIExtractor{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// openaiRequest is the request body for the OpenAI Chat Completions API.
type openaiRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
	MaxTokens   int             `json:"max_tokens"`
}

// openaiMessage represents a single message in the OpenAI conversation.
type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openaiResponse is the response body from the OpenAI Chat Completions API.
type openaiResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// callAPI sends a prompt to the OpenAI-compatible API and returns the response text.
func (e *OpenAIExtractor) callAPI(prompt string, maxTokens int) (string, error) {
	reqBody := openaiRequest{
		Model: e.model,
		Messages: []openaiMessage{
			{Role: "user", Content: prompt},
		},
		Temperature: 0.1,
		MaxTokens:   maxTokens,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling openai request: %w", err)
	}

	var responseText string

	err = retryableCtx(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/chat/completions", bytes.NewReader(jsonData))
		if err != nil {
			return fmt.Errorf("creating openai request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+e.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := e.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openai API request failed: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("reading openai response body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			err := &httpError{
				StatusCode: resp.StatusCode,
				Message:    string(body),
			}
			if retryAfter, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
				err.RetryAfter = retryAfter
			}
			return err
		}

		var apiResp openaiResponse
		if err := json.Unmarshal(body, &apiResp); err != nil {
			return fmt.Errorf("decoding openai response: %w", err)
		}

		if len(apiResp.Choices) == 0 {
			return fmt.Errorf("empty choices in openai response")
		}

		responseText = apiResp.Choices[0].Message.Content
		return nil
	}, 3)

	if err != nil {
		return "", err
	}

	return responseText, nil
}

// Classify sends a classification prompt to the OpenAI-compatible API and returns the meeting type key.
func (e *OpenAIExtractor) Classify(transcriptPreview string, templateKeys []string, classifyPrompt string) (string, error) {
	prompt := buildClassifyPrompt(classifyPrompt, transcriptPreview, templateKeys)

	text, err := e.callAPI(prompt, 32)
	if err != nil {
		return "", fmt.Errorf("openai classification failed: %w", err)
	}

	return strings.TrimSpace(text), nil
}

// Extract sends the transcript with the given extraction prompt to the OpenAI-compatible
// API and returns the raw cleaned text (markdown with YAML front-matter).
func (e *OpenAIExtractor) Extract(transcript string, extractionPrompt string) (string, error) {
	prompt := buildExtractionPrompt(extractionPrompt, transcript)

	text, err := e.callAPI(prompt, 4096)
	if err != nil {
		return "", fmt.Errorf("openai extraction failed: %w", err)
	}

	return cleanResponse(text), nil
}
