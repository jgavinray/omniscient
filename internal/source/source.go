// Package source defines the interface every meeting-platform provider
// implements. Implementations live in subpackages (e.g. googlemeet) and own
// their configuration and authentication. See docs/ADDING_A_PROVIDER.md.
package source

import (
	"context"
	"time"

	"github.com/jgavinray/omniscient/internal/models"
)

// Source fetches recent meeting transcripts from a meeting platform.
type Source interface {
	// Name returns the provider name used in dedup keys and logs ("googlemeet").
	Name() string
	// ListRecent returns transcripts modified within the given duration.
	ListRecent(ctx context.Context, since time.Duration) ([]*models.Transcript, error)
}
