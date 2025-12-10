# CortexOS – Repo Code Review Integration Plan

Goal: end-to-end, production-grade workflow `job.workflow.repo.code_review` that can review large repos (10k+ files) via the existing control plane (NATS + Redis + scheduler). This plan defines subjects, workers, data contracts, budgeting, safety, observability, and rollout order.

## 1) Subjects, Pools, Workers
- `job.workflow.repo.code_review` (orchestrator entry)
- `job.repo.scan` (pool: `repo-scan`) – builds file index; clones if needed.
- `job.repo.partition` (pool: `repo-partition`) – filters, ranks, batches files.
- `job.repo.lint` (pool: `repo-lint`) – language-specific static checks.
- `job.code.llm` (pool: `code-llm`) – existing code reviewer; reused.
- `job.chat.simple` (pool: `chat-simple`) – explanations; reused.
- `job.repo.tests` (pool: `repo-tests`) – runs repo tests.
- `job.repo.report` (pool: `repo-report`) – aggregates patches/findings/tests into report.
- Future: `job.repo.git.mr` (pool: `repo-git`) – apply patches / open MR (phase 3).

## 2) Data Contracts (Redis Context / Result)
All contexts stored as JSON under `ctx:<job_id>`, results under `res:<job_id>`.

### Scan (`job.repo.scan`)
Context:
```json
{
  "repo_url": "git@github.com:org/project.git",
  "branch": "main",
  "local_path": "/repos/project",
  "include_globs": ["**/*.go", "**/*.ts"],
  "exclude_globs": ["vendor/**", "node_modules/**", "dist/**", ".git/**"]
}
```
Result:
```json
{
  "repo_root": "/repos/project",
  "files": [
    {"path": "cmd/api/main.go", "language": "go", "bytes": 2048, "loc": 120, "recent_commits": 5}
  ]
}
```

### Partition (`job.repo.partition`)
Context:
```json
{
  "repo_root": "/repos/project",
  "files": [...],          // from scan result
  "max_files": 5000,
  "batch_size": 100,
  "strategy": "risk_first" // or "breadth_first"
}
```
Result:
```json
{
  "batches": [
    {"batch_id": "b1", "files": ["cmd/api/main.go", "internal/core/foo.go"]},
    {"batch_id": "b2", "files": ["..."]}
  ],
  "skipped": ["vendor/.../README.md"]
}
```

### Lint (`job.repo.lint`)
Context:
```json
{
  "repo_root": "/repos/project",
  "batch_id": "b1",
  "files": ["cmd/api/main.go", "internal/core/foo.go"],
  "language": "go"
}
```
Result:
```json
{
  "batch_id": "b1",
  "findings": [
    {"file_path": "cmd/api/main.go", "line": 42, "column": 5, "severity": "warning", "rule": "unused-param", "message": "parameter ctx is unused"}
  ]
}
```

### Code Review (`job.code.llm`)
Context (per file/chunk):
```json
{
  "file_path": "internal/core/foo.go",
  "code_snippet": "...",
  "instruction": "Review this code for bugs, edge cases, and readability. Suggest minimal safe changes.",
  "lint_findings": [...],
  "repo_context": {"language": "go", "module": "core"}
}
```
Result (existing schema; ensure JSON validation):
```json
{
  "file_path": "internal/core/foo.go",
  "original_code": "...",
  "instruction": "...",
  "patch": {"type": "unified_diff", "content": "diff ..."},
  "analysis": [{"type": "bug", "description": "...", "severity": "high"}]
}
```

### Tests (`job.repo.tests`)
Context:
```json
{
  "repo_root": "/repos/project",
  "test_command": "go test ./...",
  "env": {"GOFLAGS": "-count=1"}
}
```
Result:
```json
{
  "repo_root": "/repos/project",
  "command": "go test ./...",
  "exit_code": 1,
  "failed": true,
  "output": "=== RUN   TestSomething\n--- FAIL: TestSomething ...\n"
}
```

### Report (`job.repo.report`)
Context:
```json
{
  "repo_root": "/repos/project",
  "files": [
    {
      "file_path": "internal/core/foo.go",
      "patch_ptr": "redis://res:patch_job_123",
      "analysis": [...],
      "explanation_ptr": "redis://res:explain_job_456"
    }
  ],
  "tests_ptr": "redis://res:test_job_789"
}
```
Result:
```json
{
  "summary": "Overall the repo is in good shape but ...",
  "sections": [
    {"title": "High-risk issues", "items": [{"file_path": "...", "description": "...", "patch_ptr": "...", "severity": "high"}]}
  ],
  "tests_summary": {"ran": true, "failed": true, "details": "..."}
}
```

## 3) Orchestrator Flow – `job.workflow.repo.code_review`
1. Receive parent JobRequest; read context (repo inputs, budgets).
2. Submit `job.repo.scan`; wait for success.
3. Submit `job.repo.partition`; wait for success.
4. Iterate batches up to budgets (`max_batches`, `max_files`, `max_tokens`):
   - Optionally submit `job.repo.lint` for the batch; collect findings.
   - For each file (or chunk for large files):
     - Submit `job.code.llm` with code + lint findings.
     - Submit `job.chat.simple` to explain patch.
   - Collect patch/explanation pointers.
5. Optionally run `job.repo.tests` at end (controlled by config).
6. Submit `job.repo.report` with all file results + tests.
7. Mark parent job succeeded/failed with `result_ptr` to the report.

Failure handling:
- If scan/partition fail: fail parent.
- If a batch fails lint: continue but record lint failure in report.
- If code review for a file fails: mark that file failed; proceed within budget; parent fails if any high-severity file fails.
- If report generation fails: parent fails.

Budgeting:
- Inputs: `max_files`, `max_batches`, `max_llm_tokens_total`, `max_parallel_code_jobs`.
- Stop scheduling new `job.code.llm` once any budget is hit.
- Chunking rule: split files > N KB or > M lines into chunks per logical function or fixed-size windows.

State/Timeouts:
- Use existing reconciler timeouts; add topic-specific overrides for new subjects in `config/timeouts.yaml` (dispatch/running).
- Parent workflow timeout defaults: e.g., child timeout 4m, total 20m; configurable.

## 4) Pools and Config Updates
- Add to `config/pools.yaml`:
  - `job.repo.scan: repo-scan`
  - `job.repo.partition: repo-partition`
  - `job.repo.lint: repo-lint`
  - `job.repo.tests: repo-tests`
  - `job.repo.report: repo-report`
  - `job.workflow.repo.code_review: workflow`
- Add timeouts in `config/timeouts.yaml` for new subjects (scan/partition/lint/tests/report/workflow).
- Add env toggles for orchestrator:
  - `REPO_REVIEW_MAX_FILES` (default 500)
  - `REPO_REVIEW_BATCH_SIZE` (default 50)
  - `REPO_REVIEW_MAX_BATCHES` (default 5)
  - `REPO_REVIEW_RUN_TESTS` (default false)

## 5) Safety & Policy
- Safety Kernel: allow only job.* topics; deny any repo URL without allowlist/host filter (basic hostname allowlist). Require `tenant_id` env var. Deny if repo URL missing and local_path missing.
- Workers must respect include/exclude globs to avoid exfiltration of vendor/build artifacts.
- Git/MR worker (phase 3) must be explicitly allowed per tenant and reject push to protected branches unless policy OKs.

## 6) Metrics & Observability
- Scheduler/gateway: existing Prom metrics cover dispatch/complete/deny.
- New per-worker metrics: duration, files processed, batches processed, lint findings count, test exit_code.
- Orchestrator: counters for batches processed, files reviewed, code-llm failures, budget caps reached, tests run.
- Logging: include `workflow_id`, `batch_id`, `file_path` in log fields; trace_id already present.

## 7) Testing Strategy
- Unit tests:
  - Repo scan/partition logic (filtering, batching, risk ranking heuristics).
  - Orchestrator control flow (fake bus/job store) for success and failure paths.
  - Report assembly with fake pointers.
- Integration/smoke:
  - Script under `tools/scripts` to submit a repo review against a small fixture repo.
  - Optionally docker-compose profile that mounts a sample repo for scan.
- Contract tests: validate JSON schemas for code-llm/report outputs; reject invalid patches.

## 8) Rollout Steps (full integration, not half measures)
1. Add subjects/pools/timeouts configs.
2. Implement workers:
   - repo-scan (git clone + walk + language/LOC/churn)
   - repo-partition (filter + risk rank + batching)
   - repo-lint (start with Go; noop for others but structured output)
   - repo-tests (generic command runner)
   - repo-report (LLM summariser using chat model)
3. Extend orchestrator for `job.workflow.repo.code_review` with budgeting, chunking, error propagation, and report emission.
4. Wire API Gateway to accept repo review submissions (REST + gRPC) with the required context fields.
5. Add safety checks for repo URLs/tenants.
6. Add smoke script for end-to-end submission.
7. Run gofmt, go test ./..., and docker-compose smoke.

## 9) Open Questions / Decisions
- Where to store cloned repos? (tmp vs persistent cache; cleanup policy).
- Language coverage for lint/test (start with Go + JS/TS?).
- Max file size/chunk size defaults (e.g., 800 lines or 50 KB per chunk).
- Which code LLM/model (local vs hosted) and schema enforcement (grammar-based?).

This plan maps directly to the current architecture (NATS subjects, Redis job store, orchestrator style) and is ready to implement end-to-end without stubs. 
