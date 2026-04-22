# OpenAPI Audit Baseline

Captured: 2026-04-18
Task: task-f2143638 (OpenAPI spec audit and completion, 100% coverage)
Spec under audit: `docs/api/openapi/cordum-api.yaml` @ commit `6d9760b`

This file records the pre-task state of the route↔spec diff so the reviewer
can verify that all post-task changes were purely additive (the
"zero breaking changes from previous spec" DoD item).

## Method

`go run ./tools/openapi-audit --spec docs/api/openapi/cordum-api.yaml --gateway-dir core/controlplane/gateway`

## Results

- **Routes seen:** 162
- **Spec ops seen:** 147
- **Routes missing from spec:** 19
- **Spec ops without a route:** 3

### Routes missing from spec (19)

The planning notes anticipated 7 missing ops; the live tree is broader
because several features shipped after the plan was written without the
spec being updated in lockstep.

| Method | Path | Registered at |
|--------|------|---------------|
| GET    | /api/v1/agents                    | gateway.go:944 |
| POST   | /api/v1/agents                    | gateway.go:945 |
| GET    | /api/v1/agents/{id}               | gateway.go:946 |
| PUT    | /api/v1/agents/{id}               | gateway.go:947 |
| DELETE | /api/v1/agents/{id}               | gateway.go:948 |
| GET    | /api/v1/agents/{id}/stats         | gateway.go:949 |
| GET    | /api/v1/agents/{id}/tools         | gateway.go:950 |
| GET    | /api/v1/agents/{id}/denied-events | gateway.go:951 |
| GET    | /api/v1/audit/verify              | gateway.go:989 |
| GET    | /api/v1/mcp/tools                 | gateway.go:952 |
| POST   | /api/v1/mcp/verify-signature      | gateway.go:955 |
| GET    | /api/v1/mcp/approvals             | gateway.go:1096 |
| GET    | /api/v1/mcp/approvals/{id}        | gateway.go:1097 |
| POST   | /api/v1/mcp/approvals/{id}/approve| gateway.go:1098 |
| POST   | /api/v1/mcp/approvals/{id}/reject | gateway.go:1099 |
| POST   | /api/v1/policy/shadows/{id}       | gateway.go:1125 |
| GET    | /api/v1/policy/shadows/{id}       | gateway.go:1126 |
| DELETE | /api/v1/policy/shadows/{id}       | gateway.go:1127 |
| ANY    | /api/v1/stream                    | gateway.go:1150 |

### Spec ops without a route (3)

All three live under `/api/v1/policy/bundles/{id}/shadow`, the shadow
activation path that was relocated to `/api/v1/policy/shadows/{id}` during
task-0f2ba204 / task-18d9f782 follow-up to avoid a Go 1.22 mux pattern
collision with `/api/v1/policy/bundles/snapshots/{id}`. The spec was not
updated when the routes moved.

| Method | Path |
|--------|------|
| POST   | /api/v1/policy/bundles/{id}/shadow |
| GET    | /api/v1/policy/bundles/{id}/shadow |
| DELETE | /api/v1/policy/bundles/{id}/shadow |

Step 5 re-homes these to `/api/v1/policy/shadows/{id}` rather than deleting
them outright; the old path entries are kept with `deprecated: true` per
the triple-check-deletions guidance, so clients that cached the old URLs
get an explicit deprecation signal rather than a silent 404-from-spec.

## oasdiff baseline

`oasdiff` is not installed on the worker's local machine; on CI the
`openapi` job runs `go install github.com/tufin/oasdiff@latest` and then
invokes `oasdiff breaking --fail-on ERR <base> <current>`. The baseline
oasdiff run against the pre-task tree is necessarily empty (the baseline
file IS the current file), which is the correct "before" evidence: any
post-task oasdiff invocation comparing `origin/main:...yaml` against the
final spec will surface exactly the additive changes made during this
task, and the ERR-level gate will fail the build if any of them narrow an
existing response or remove a documented op.

## Interpretation

The 19 missing routes + 3 renamed ops cover every gap the audit tool can
see today. The subsequent steps address them one by one; the post-task
re-run of `openapi-audit` must exit 0 for the task to be considered
complete (step-12).
