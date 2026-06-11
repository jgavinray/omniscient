// Package destination defines the interface every knowledge-base provider
// implements. Implementations live in subpackages (e.g. confluence) and own
// their configuration and authentication. See docs/ADDING_A_PROVIDER.md.
package destination

import (
	"context"

	"github.com/jgavinray/omniscient/internal/models"
)

// Destination publishes extracted meeting notes to a knowledge base.
type Destination interface {
	// Name returns the provider name used in logs and published-URL maps.
	Name() string
	// Publish creates or updates a page for the transcript's notes and
	// returns its URL. Publish MUST be idempotent: publishing the same
	// transcript twice must update the same page, not create a duplicate —
	// the pipeline relies on this to retry safely after partial failures.
	Publish(ctx context.Context, result *models.ExtractionResult, t *models.Transcript) (string, error)
}
