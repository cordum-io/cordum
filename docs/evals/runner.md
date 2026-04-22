# Eval dataset runner

The eval runner is the execution half of Cordum's phase-2 governance
regression pipeline:

1. an immutable **eval dataset** captures curated job-request snapshots,
2. the **runner** replays each entry through the current or candidate policy,
3. the gateway stores a durable **RunResult** with per-entry outcomes and a
   summary score.

The HTTP surface is:

- `POST /api/v1/evals/datasets/{id}/run`
- `GET /api/v1/evals/datasets/{id}/runs`
- `GET /api/v1/evals/runs/{run_id}`
- `DELETE /api/v1/evals/runs/{run_id}?force=true`

## Execution pipeline

The runner is intentionally layered:

1. **Dataset lookup** — the gateway loads the immutable dataset from the
   eval-dataset store.
2. **Policy resolution** — the handler builds either the active merged policy
   or a candidate policy using the existing policy-bundle loader.
3. **Shared replay primitives** — the handler reuses the
   `core/controlplane/policyreplay` decision-comparison helpers so the drift
   language (`escalated`, `relaxed`, `unchanged`) matches the rest of the
   governance surface.
4. **Entry evaluation** — `core/evals/runner` converts each entry snapshot into
   a minimal `JobRequest`, evaluates it, and classifies the result as
   `pass`, `fail`, `regression`, or `error`.
5. **History persistence** — the completed `RunResult` is stored in the
   eval-run history store with timestamps for later auditing and pagination.

Small runs (≤500 evaluated entries) complete synchronously in the POST
response. Larger runs return `202 Accepted` with a `run_id`; poll
`GET /api/v1/evals/runs/{run_id}` until the run completes.

## Regression semantics

The epic rail is strict:

> regression = a previously-denied action is now allowed by the new policy

Concretely, the runner labels an entry as a **regression** when:

- the expected decision is one of `deny`, `require_approval`, `throttle`, or
  `allow_with_constraints`, and
- the actual evaluated decision becomes `allow`.

Everything else is either:

- `pass` — expected decision exactly matched the evaluated decision
- `fail` — changed, but not in the regression direction
- `error` — input snapshot could not be built or evaluation failed

## CI / release-gate usage

`cordumctl evals run --dataset <id> --use-current --wait` prints the summary
and exits non-zero when `regressions > 0`, which makes it suitable as a policy
promotion gate.

### GitHub Actions example

```yaml
- name: Replay eval dataset against candidate policy
  run: |
    cordumctl evals run \
      --gateway "$CORDUM_GATEWAY" \
      --api-key "$CORDUM_API_KEY" \
      --tenant "$CORDUM_TENANT_ID" \
      --dataset "$EVAL_DATASET_ID" \
      --candidate-content "@policy/candidate.yaml" \
      --wait
```

Because the CLI exits non-zero on regressions, the workflow fails immediately
when a candidate policy relaxes a previously blocked action.

## Operational notes

- Runs are tenant-scoped and history is queryable per dataset.
- `DELETE ...?force=true` is an admin escape hatch for hygiene/offboarding, not
  the normal lifecycle.
- The runner does **not** own the dashboard drill-down UI; it only produces the
  per-entry data that the sibling dashboard task consumes.
