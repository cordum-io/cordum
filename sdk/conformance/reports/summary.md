# SDK Conformance — last run

_Generated: 2026-04-21T06:45:42.759Z_

## Totals

| SDK | Pass | Fail | Total |
|-----|------|------|-------|
| go | 20 | 0 | 20 |
| python | 20 | 0 | 20 |
| typescript | 20 | 0 | 20 |

## Matrix

| Fixture | Go | Python | TypeScript |
|---------|-----|--------|------------|
| agents.create_and_get | PASS | PASS | PASS |
| agents.list_and_filter | PASS | PASS | PASS |
| agents.update_and_delete | PASS | PASS | PASS |
| audit.list_paginated | PASS | PASS | PASS |
| auth.api_key_success | PASS | PASS | PASS |
| auth.api_key_unauthorized | PASS | PASS | PASS |
| auth.session_login_refresh | PASS | PASS | PASS |
| errors.not_found | PASS | PASS | PASS |
| errors.rate_limit_retry_after | PASS | PASS | PASS |
| errors.server_retry_exhausted | PASS | PASS | PASS |
| errors.validation_field_errors | PASS | PASS | PASS |
| idempotency.post_with_key_retries | PASS | PASS | PASS |
| idempotency.post_without_key_does_not_retry | PASS | PASS | PASS |
| jobs.cancel_inflight | PASS | PASS | PASS |
| jobs.list_with_filters | PASS | PASS | PASS |
| jobs.submit_and_track | PASS | PASS | PASS |
| policies.apply_and_rollback | PASS | PASS | PASS |
| policies.list_and_get_bundle | PASS | PASS | PASS |
| workflows.crud | PASS | PASS | PASS |
| workflows.run_and_stream_events | PASS | PASS | PASS |

**Overall:** ✅ ALL GREEN — every SDK passed every fixture.
