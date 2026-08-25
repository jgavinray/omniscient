package publish

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/jgavinray/omniscient/internal/models"
)

// LocalSink writes extraction results as markdown files (YAML front-matter +
// body) under a directory on disk. One file per transcript, named
// "<date>_<slug>.md". Writes are atomic (temp file + rename) so a crashed
// run never leaves a half-written file behind. Re-publishing overwrites.
type LocalSink struct {
	outputDir string
}

// NewLocalSink creates a LocalSink writing to outputDir (created on demand).
func NewLocalSink(outputDir string) *LocalSink {
	return &LocalSink{outputDir: outputDir}
}

// Name implements Sink.
func (s *LocalSink) Name() string { return "local" }

// slugify converts a transcript name into a filesystem-safe slug:
// lowercase, non-alphanumeric runs collapsed to '-', trimmed, capped at 40
// characters. Empty input yields "".
func slugify(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	prevDash := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// Publish implements Sink. It writes the full extraction result (re-marshal
// of the parsed front-matter plus the markdown body) to
// <outputDir>/<date>_<slug>.md and returns the absolute file path.
func (s *LocalSink) Publish(ctx context.Context, result *models.ExtractionResult, transcriptName string) (string, error) {
	// Respect cancellation before starting the write.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	date := frontMatterDate(result.FrontMatter)
	slug := slugify(stripExt(transcriptName))
	if slug == "" {
		slug = "transcript"
	}
	filename := fmt.Sprintf("%s_%s.md", date, slug)

	absDir, err := filepath.Abs(s.outputDir)
	if err != nil {
		return "", fmt.Errorf("resolve output dir %s: %w", s.outputDir, err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return "", fmt.Errorf("create output dir %s: %w", absDir, err)
	}
	dest := filepath.Join(absDir, filename)

	fmBytes, err := yaml.Marshal(result.FrontMatter)
	if err != nil {
		return "", fmt.Errorf("marshal front-matter: %w", err)
	}

	content := "---\n" + string(fmBytes) + "---\n\n" + result.Markdown

	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("write local file %s: %w", dest, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("rename local file to %s: %w", dest, err)
	}

	slog.Info("published local file", "path", dest)
	return dest, nil
}
