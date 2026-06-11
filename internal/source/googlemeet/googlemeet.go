package googlemeet

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/jgavinray/omniscient/internal/models"
	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// SourceName is the provider name used in dedup keys, config, and logs.
const SourceName = "googlemeet"

// Source provides authenticated access to Google Meet transcripts, which
// Google Meet saves as Google Docs in a Drive folder.
type Source struct {
	service  *drive.Service
	folderID string
}

// New creates an authenticated Google Meet source. It loads the OAuth config
// from credentialsPath and the saved token from tokenPath, then constructs a
// Drive service with an auto-refreshing token source.
//
// The token must already exist — run `omniscient auth googlemeet` first.
func New(ctx context.Context, credentialsPath, tokenPath, folderID string) (*Source, error) {
	config, err := loadOAuthConfig(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("loading OAuth config: %w", err)
	}

	token, err := loadToken(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("loading token: %w", err)
	}
	if token == nil {
		return nil, fmt.Errorf("no token found at %s — run 'omniscient auth googlemeet' first to authorize", tokenPath)
	}

	tokenSource := getTokenSource(ctx, config, tokenPath, token)
	httpClient := oauth2.NewClient(ctx, tokenSource)

	service, err := drive.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("creating Drive service: %w", err)
	}

	slog.Info("Google Meet source initialized")

	return &Source{service: service, folderID: folderID}, nil
}

// Name implements source.Source.
func (s *Source) Name() string { return SourceName }

// ListRecent fetches transcript documents from the configured folder that
// were modified within the given duration. Each document is exported as
// plain text. If a single file export fails, the error is logged and that
// file is skipped — remaining files are still processed.
func (s *Source) ListRecent(ctx context.Context, since time.Duration) ([]*models.Transcript, error) {
	cutoff := time.Now().UTC().Add(-since)
	cutoffStr := cutoff.Format(time.RFC3339)

	query := fmt.Sprintf(
		"mimeType='application/vnd.google-apps.document' and modifiedTime > '%s' and '%s' in parents and trashed = false",
		cutoffStr, s.folderID,
	)

	slog.Info("querying Google Drive for recent transcripts",
		"folder_id", s.folderID,
		"since", cutoffStr,
	)

	var transcripts []*models.Transcript
	pageToken := ""

	for {
		call := s.service.Files.List().
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

			content, err := s.exportFileAsText(ctx, file.Id)
			if err != nil {
				slog.Warn("failed to export file as text, skipping",
					"file_id", file.Id,
					"file_name", file.Name,
					"error", err,
				)
				continue
			}

			transcripts = append(transcripts, &models.Transcript{
				ID:         file.Id,
				Source:     SourceName,
				Title:      file.Name,
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
		"folder_id", s.folderID,
	)

	return transcripts, nil
}

// exportFileAsText exports a Google Docs file as plain text.
func (s *Source) exportFileAsText(ctx context.Context, fileID string) (string, error) {
	resp, err := s.service.Files.Export(fileID, "text/plain").Context(ctx).Download()
	if err != nil {
		return "", fmt.Errorf("exporting file %s as text/plain: %w", fileID, err)
	}
	defer resp.Body.Close()

	const maxTranscriptBytes = 50 * 1024 * 1024 // 50 MB
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTranscriptBytes))
	if err != nil {
		return "", fmt.Errorf("reading exported content for file %s: %w", fileID, err)
	}
	if int64(len(data)) >= maxTranscriptBytes {
		return "", fmt.Errorf("exported content for file %s exceeded %d byte limit", fileID, maxTranscriptBytes)
	}

	content := strings.TrimSpace(string(data))
	return content, nil
}
