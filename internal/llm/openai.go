package llm

import (
	"bytes"
	"context"
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
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return fmt.Errorf("unexpected redirect to %s", req.URL)
			},
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
func (e *OpenAIExtractor) callAPI(ctx context.Context, prompt string, maxTokens int) (string, error) {
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

		const maxResponseBytes = 10 * 1024 * 1024 // 10 MB
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		if err != nil {
			return fmt.Errorf("reading openai response body: %w", err)
		}
		if int64(len(body)) >= maxResponseBytes {
			return fmt.Errorf("openai response exceeded %d byte limit", maxResponseBytes)
		}

		if resp.StatusCode != http.StatusOK {
			err := &httpError{
				StatusCode: resp.StatusCode,
				Message:    truncateBody(string(body)),
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

// classifyMaxTokens is the output budget for the one-word Classify call.
// It is deliberately far larger than the reply: thinking models (e.g.
// qwen3.8-27b served by sglang) spend most of the budget on reasoning
// tokens before emitting the answer — at 32 tokens the reply came back
// empty. The budget is a cap, not a target: the model stops reasoning on
// its own (a classify prompt used ~60 reasoning tokens), so the usual
// call still costs a few tokens, not 8k.
const classifyMaxTokens = 8192

// Classify sends a classification prompt to the OpenAI-compatible API and returns the meeting type key.
func (e *OpenAIExtractor) Classify(ctx context.Context, transcriptPreview string, templateKeys []string, classifyPrompt string) (string, error) {
	prompt := buildClassifyPrompt(classifyPrompt, transcriptPreview, templateKeys)

	text, err := e.callAPI(ctx, prompt, classifyMaxTokens)
	if err != nil {
		return "", fmt.Errorf("openai classification failed: %w", err)
	}

	return strings.TrimSpace(text), nil
}

// Extract sends the transcript with the given extraction prompt to the OpenAI-compatible
// API and returns the raw cleaned text (markdown with YAML front-matter).
func (e *OpenAIExtractor) Extract(ctx context.Context, transcript string, extractionPrompt string) (string, error) {
	prompt := buildExtractionPrompt(extractionPrompt, transcript)

	text, err := e.callAPI(ctx, prompt, 4096)
	if err != nil {
		return "", fmt.Errorf("openai extraction failed: %w", err)
	}

	return cleanResponse(text), nil
}
