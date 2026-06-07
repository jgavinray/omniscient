# 02 Confluence URL Normalization

## Intent

Prevent malformed Confluence API URLs when users configure either the Atlassian
site root or the common `/wiki` browser root.

## Requirements

- Accept `https://company.atlassian.net`.
- Accept `https://company.atlassian.net/wiki`.
- Normalize both forms so API requests use exactly one
  `/wiki/rest/api/content...` prefix.
- Reject unrelated pathful base URLs, such as `https://company.atlassian.net/foo`,
  when Confluence publishing is enabled.
- Preserve HTTPS-only validation for enabled Confluence.
- Returned page URLs should remain valid browser URLs.

## Non-Goals

- Do not support arbitrary Confluence reverse-proxy path prefixes.
- Do not change the `confluence.base_url` config field.
- Do not rewrite Confluence REST endpoints beyond avoiding doubled `/wiki`.

## Likely Files

- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/confluence/publisher.go`
- `internal/confluence/publisher_test.go`
- `config.yaml.example` only if the comment needs clarification.

## Acceptance Tests

- Client configured with `https://host` calls `/wiki/rest/api/content`.
- Client configured with `https://host/wiki` calls `/wiki/rest/api/content`, not
  `/wiki/wiki/rest/api/content`.
- Config validation rejects `https://host/foo` when Confluence is enabled.
- Existing Confluence publish tests still pass.

## Suggested Claude Prompt

Implement only `docs/reliability-operability/02-confluence-url-normalization.md`.
Work only in the current repo. Do not use parent paths. Run `gofmt` and
`go test ./internal/config ./internal/confluence`.
