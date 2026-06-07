# Omniscient Release Handoff

## Implemented Feature Summary

### 1. Explicit Publish State and Transcript Lifecycle ✅
- Implemented bounded transcript statuses: `discovered`, `extracted`, `published`, `skipped`, `failed`
- Added comprehensive transcript lifecycle tracking with timestamps and attempt counts
- Safe migration from old `processed_transcripts` table to new schema
- Database state persistence is now atomic and idempotent

### 2. Confluence Base URL Normalization and Validation ✅
- URL normalization accepts both `https://company.atlassian.net` and `https://company.atlassian.net/wiki`
- Validates against malformed URLs and rejects unsupported path prefixes
- Ensures API calls use correct `/wiki/rest/api/content` endpoint

### 3. Correct Sync Success Accounting ✅
- Publish success only counted when database state persistence succeeds
- Separate accounting for publish successes, persistence failures, skipped transcripts, and failed transcripts
- Returns appropriate errors when side effects occur but state persistence fails

### 4. Testable Sync Orchestration ✅
- Introduced sync service with dependency injection through interfaces
- Thin CLI command construction delegates to service layer
- Tests use fake dependencies to exercise actual sync service
- Maintains recognizable CLI behavior while improving testability

### 5. Context-Aware Retry and Backoff Hygiene ✅
- Retry sleeps respect context cancellation for graceful shutdown
- HTTP 429 responses respect `Retry-After` header with bounded jitter
- Permanent errors (401/403) are not retried
- Deterministic retry behavior with bounded backoff schedule

### 6. Operator Commands ✅
- `omniscient status` shows transcript counts by status and recent transcript states
- `retry-failed` resets failed transcripts to eligible state
- `forget <transcript-id>` removes transcript records for reprocessing
- Plain text output suitable for cron/admin automation

### 7. Prompt and Template Validation ✅
- Validates prompt templates during config loading
- Requires `{{TRANSCRIPT}}` in extraction prompts
- Requires `{{TEMPLATE_KEYS}}` and `{{TRANSCRIPT_PREVIEW}}` in classify prompts
- Clear validation errors identify offending template keys

### 8. Observability, Run IDs, and Safe Logging ✅
- Unique run ID generated for each sync invocation
- Append-only sync events with bounded metadata
- No transcript content logging in normal operation
- Dry-run preview remains bounded and safe

## Verification Commands

The following verification commands were executed successfully:

```bash
gofmt -w <touched Go files>
go test ./cmd/omniscient
go build ./cmd/omniscient
go test ./...
go build ./...
```

**Note**: Staging validation is still required before production deployment.

## Operational Validation Checklist

### Pre-Deployment
- [ ] Configuration file validates successfully
- [ ] Database schema migration works on fresh install
- [ ] Confluence URL normalization functions correctly
- [ ] Prompt templates contain required placeholders

### Post-Deployment
- [ ] Sync command processes transcripts without errors
- [ ] Status command shows correct counts and states
- [ ] Failed transcripts are properly marked and retryable
- [ ] Dry-run mode skips publishing but shows preview
- [ ] Confluence-disabled mode skips publishing but maintains state

### Monitoring
- [ ] Run IDs appear in sync logs and events
- [ ] Event metadata is bounded and safe
- [ ] Persistence failures are logged and reported
- [ ] Transcript content is not logged in normal operation

## Release Checklist

### Pre-Release
- [ ] Update version string in `cmd/omniscient/main.go`
- [ ] Run focused test suite: `go test ./cmd/omniscient`
- [ ] Verify build: `go build ./cmd/omniscient`
- [ ] Check formatting: `gofmt -w .`
- [ ] Update documentation with any breaking changes

**Note**: This version is still in development and should not be tagged as 1.0.0 until additional validation is completed.

### Deployment
- [ ] Backup existing database if present
- [ ] Deploy binary to staging environment for validation
- [ ] Verify configuration file is valid
- [ ] Test basic sync functionality with dry-run
- [ ] Monitor logs for any errors
- [ ] Validate against production data in staging before full deployment

### Post-Release
- [ ] Monitor status command output for expected counts in staging
- [ ] Verify retry-failed command works correctly
- [ ] Check that forget command removes records as expected
- [ ] Monitor Confluence publishing for successful posts
- [ ] Gradually promote to production after staging validation

## Known Caveats

1. **Database Migration**: Existing databases with old schema are automatically migrated, but manual verification is recommended for large datasets.

2. **Rate Limiting**: LLM and Confluence API calls use retry logic, but extreme rate limiting may cause delays.

3. **Large Transcripts**: Very long transcripts (>100k chars) may be truncated during classification, but full content is used for extraction.

4. **Network Dependencies**: Requires reliable internet connectivity to Google Drive, LLM providers, and Confluence.

5. **Resource Usage**: Memory usage scales with transcript count; large batches may require increased memory limits.

6. **Security**: No sensitive transcript content is logged during normal operation, but configuration files should be protected from unauthorized access.

7. **Development Status**: This is still a development version and should be considered ready for PR review and staging validation rather than production-ready.

## Rollback/Recovery Notes

### Status Check
```bash
omniscient status --config /opt/omniscient/config.yaml
```

### Retry Failed Transcripts
```bash
omniscient retry-failed --config /opt/omniscient/config.yaml
```

### Forget Problematic Transcripts
```bash
omniscient forget <transcript-id> --config /opt/omniscient/config.yaml
```

### Rollback Procedure
1. Stop the service if running as a daemon
2. Backup the database file
3. Revert to previous binary version
4. Restore database from backup if needed
5. Test basic functionality with dry-run mode

### Recovery from Partial Failures
- Use `status` command to identify failed transcripts
- Use `retry-failed` to reset failed state
- Use `forget` to remove problematic records for reprocessing
- Monitor logs for persistence failures and address underlying issues

## Support Contacts

- Development: jgavinray
- Documentation: See `docs/RELIABILITY_OPERABILITY_REQUIREMENTS.md` for detailed implementation notes
- Issue Tracking: Use GitHub issues for bugs and feature requests
- **Note**: This is a development version. For production deployment, additional validation and security hardening should be completed.