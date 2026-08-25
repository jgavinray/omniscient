package publish

import (
	"context"

	"github.com/jgavinray/omniscient/internal/confluence"
	"github.com/jgavinray/omniscient/internal/models"
)

// ConfluenceSink wraps the Confluence REST client to satisfy the Sink
// interface. It publishes each summary as a Confluence page, creating or
// updating by title.
type ConfluenceSink struct {
	client       *confluence.Client
	spaceKey     string
	parentPageID string
}

// NewConfluenceSink wraps an existing Confluence client.
func NewConfluenceSink(client *confluence.Client, spaceKey, parentPageID string) *ConfluenceSink {
	return &ConfluenceSink{
		client:       client,
		spaceKey:     spaceKey,
		parentPageID: parentPageID,
	}
}

// Name implements Sink.
func (s *ConfluenceSink) Name() string { return "confluence" }

// Publish implements Sink. It delegates to the underlying Confluence client
// and returns the page URL.
func (s *ConfluenceSink) Publish(ctx context.Context, result *models.ExtractionResult, transcriptName string) (string, error) {
	return s.client.PublishMarkdown(ctx, s.spaceKey, s.parentPageID, result, transcriptName)
}
