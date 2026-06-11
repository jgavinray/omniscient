# Adding a Provider

Omniscient is a pipeline: **sources** (meeting platforms) produce transcripts,
**destinations** (knowledge bases) receive extracted notes. Each provider is
one package implementing one small interface. This guide walks through adding
either kind. `internal/source/googlemeet` and `internal/destination/confluence`
are the reference implementations — copy their shape.

```
 sources (1..n)          pipeline                 destinations (1..n)
┌──────────────┐   ┌──────────────────────┐   ┌──────────────────┐
│ googlemeet   │──▶│ dedup → classify →   │──▶│ confluence       │
│ zoom (you?)  │   │ extract → validate → │   │ notion (you?)    │
└──────────────┘   │ publish → mark       │   └──────────────────┘
                   └──────────────────────┘
                        SQLite state + events
```

## Adding a Source (e.g. Zoom)

1. **Create the package:** `internal/source/zoom/zoom.go`, `package zoom`.

2. **Implement the interface** (`internal/source/source.go`):

   ```go
   type Source interface {
       Name() string
       ListRecent(ctx context.Context, since time.Duration) ([]*models.Transcript, error)
   }
   ```

   - `Name()` — return a short stable key, e.g. `"zoom"`. It becomes part of
     dedup keys (`zoom:<id>`); **never change it once shipped** or every
     transcript will reprocess.
   - `ListRecent(ctx, since)` — return `[]*models.Transcript` with `Source`
     set to your name, the provider-native `ID`, a human-readable `Title`,
     `ModifiedAt`, and plain-text `Content`. Skip-and-log individual fetch
     failures (`slog.Warn`); only return an error when the whole listing
     fails.

3. **Add config:** a new struct in `internal/config/config.go` under
   `SourcesConfig` with an `Enabled *bool` + `IsEnabled()` method (copy
   `GoogleMeetConfig`). Validate its fields in `validate()` **only when
   enabled**, and count it in the `enabledSources` check.

4. **Wire it:** append a block in `buildSources()` in
   `cmd/omniscient/sync.go`.

5. **Test it:** use `httptest.NewServer` to fake the provider API (see
   `internal/destination/confluence/publisher_test.go` for the pattern).
   Cover: happy-path field mapping, listing failure, individual-item failure.

6. **Document it:** add the section to `config.yaml.example` and update the
   provider matrix in `README.md`.

## Adding a Destination (e.g. Notion)

Same steps with `internal/destination/`, `DestinationsConfig`, and
`buildDestinations()`. The interface:

```go
type Destination interface {
    Name() string
    Publish(ctx context.Context, result *models.ExtractionResult, t *models.Transcript) (string, error)
}
```

Two contract requirements:

- **Idempotency (required):** `Publish` must create-or-update keyed on a
  stable identity (Confluence uses the page title). After a partial failure
  the pipeline retries the *whole* transcript against *all* destinations and
  relies on idempotency to avoid duplicate pages.
- **Return the canonical page URL;** it is stored in SQLite (as a
  `{"name": "url"}` JSON map) and shown by `omniscient status`.

`result.FrontMatter` is the parsed YAML map (date, participants, …);
`result.Markdown` is the notes body. Convert markdown however your platform
needs — Confluence renders it to HTML with goldmark; a Notion implementation
would map it to blocks.

## Error-handling rules (all providers)

Follow the project's three categories:

1. **Transient** (HTTP 429, 5xx, timeouts): retry with `internal/retry`
   (3 attempts, exponential backoff, honors `Retry-After`).
2. **Permanent** (401, bad config): return the error immediately.
3. **Per-item failures** during listing: log with `slog.Warn`, skip, continue.

## PR checklist

- [ ] Interface implemented in its own package under `internal/source/` or `internal/destination/`
- [ ] `Name()` is short, lowercase, stable
- [ ] Config struct + validation (only when enabled) + `config.yaml.example` entry
- [ ] Wired into `buildSources`/`buildDestinations` in `cmd/omniscient/sync.go`
- [ ] `httptest`-based tests, including failure cases
- [ ] README provider matrix updated
- [ ] `make test` green
