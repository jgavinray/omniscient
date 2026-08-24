package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jgavinray/omniscient/internal/models"
	"github.com/jgavinray/omniscient/internal/retry"
)

const slackMaxTextLen = 3500

// SlackSink publishes extraction results to a Slack channel via an incoming
// webhook. The webhook URL is configured in config.yaml under slack.webhook_url.
type SlackSink struct {
	webhookURL string
	httpClient *http.Client
}

// NewSlackSink creates a SlackSink targeting the given incoming-webhook URL.
func NewSlackSink(webhookURL string) *SlackSink {
	return &SlackSink{
		webhookURL: webhookURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Name implements Sink.
func (s *SlackSink) Name() string { return "slack" }

// buildSlackPayload assembles the JSON payload for the Slack webhook.
// It produces a single attachment card with the meeting title, date, and a
// truncated markdown body (Slack caps message text at ~4000 chars).
func buildSlackPayload(result *models.ExtractionResult, transcriptName string) map[string]any {
	title := fmt.Sprintf("%s - %s", stripExt(transcriptName), frontMatterDate(result.FrontMatter))
	body := strings.TrimSpace(result.Markdown)
	if len(body) > slackMaxTextLen {
		body = body[:slackMaxTextLen] + "…"
	}
	return map[string]any{
		"text": "New meeting summary published by Omniscient",
		"attachments": []map[string]any{
			{
				"color":     "#4396fc",
				"title":     title,
				"text":      body,
				"mrkdwn_in": []string{"title", "text"},
			},
		},
	}
}

// Publish implements Sink. It posts the summary to the configured Slack
// channel. Transient errors (429, 5xx) are retried up to 3 times with
// exponential backoff via retry.DoContext, which honours context
// cancellation so a shutdown aborts in-flight webhook attempts promptly.
func (s *SlackSink) Publish(ctx context.Context, result *models.ExtractionResult, transcriptName string) (string, error) {
	payload, err := json.Marshal(buildSlackPayload(result, transcriptName))
	if err != nil {
		return "", fmt.Errorf("marshal slack payload: %w", err)
	}

	var respBody []byte
	err = retry.DoContext(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("create slack request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("execute slack request: %w", err)
		}
		defer resp.Body.Close()

		respBody, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read slack response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return &retry.HTTPError{
				StatusCode: resp.StatusCode,
				Message:    string(respBody),
			}
		}
		return nil
	}, 3)

	if err != nil {
		return "", fmt.Errorf("post to slack webhook: %w", err)
	}

	title := fmt.Sprintf("%s - %s", stripExt(transcriptName), frontMatterDate(result.FrontMatter))
	slog.Info("published to slack", "title", title)
	return "slack:" + title, nil
}
