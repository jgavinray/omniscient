// Package publish defines the Sink abstraction used by the sync pipeline to
// route extracted meeting summaries to one or more destinations (Confluence,
// Slack, and local files). Each sink is idempotent where the backend allows:
// re-publishing the same transcript either updates or overwrites the prior
// artifact, so retrying a failed run does not create orphaned duplicates.
package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jgavinray/omniscient/internal/models"
)

// Sink is a publication destination for extracted meeting summaries.
type Sink interface {
	// Name returns a stable, lowercase identifier for this sink
	// (e.g. "confluence", "slack", "local").
	Name() string

	// Publish routes an extraction result to this destination and returns a
	// reference (URL or file path) describing where the content was stored.
	// Implementations must be safe to re-invoke for the same transcript and
	// must respect context cancellation (retries and HTTP calls are ctx-aware
	// so a shutdown between transcripts aborts promptly).
	Publish(ctx context.Context, result *models.ExtractionResult, transcriptName string) (string, error)
}

// Result records where a single transcript was published. It is serialized to
// JSON and persisted alongside the transcript id so runs are idempotent and
// auditable.
type Result struct {
	Sink string `json:"sink"`
	Ref  string `json:"ref"`
}

// MarshalResults serializes sink results into the JSON string stored in the
// database. An empty slice marshals to "[]" so skipped transcripts are
// explicitly recorded as having no published destinations.
func MarshalResults(results []Result) (string, error) {
	if len(results) == 0 {
		return "[]", nil
	}
	data, err := json.Marshal(results)
	if err != nil {
		return "", fmt.Errorf("marshal sink results: %w", err)
	}
	return string(data), nil
}

// frontMatterDate extracts the "date" value from parsed front-matter, falling
// back to today's date when absent. This mirrors the Confluence publisher's
// title logic so all sinks agree on the meeting date.
func frontMatterDate(fm map[string]any) string {
	if d, ok := fm["date"]; ok {
		if s, ok := d.(string); ok && s != "" {
			return s
		}
	}
	return time.Now().Format("2006-01-02")
}

// stripExt removes well-known file extensions from a transcript name so sink
// titles and filenames read cleanly.
func stripExt(name string) string {
	for _, ext := range []string{".gdoc", ".docx", ".doc", ".txt", ".pdf"} {
		name = strings.TrimSuffix(name, ext)
	}
	return name
}
