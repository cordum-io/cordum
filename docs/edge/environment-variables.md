# Cordum Edge environment variables

Consolidated operator reference for Cordum Edge shadow-detection,
retention, runtime-ingest, and managed-policy environment variables.

**Authoritative source: code.** When this doc and the code diverge, the
code wins; file a doc-fix task. Verify a current value with
`grep -rn '<NAME>' core/ cmd/` from the `cordum/` repo root.

This file is a sibling of [`configuration.md`](configuration.md), which
covers Gateway credentials, agentd transport, heartbeat, policy mode,
session export, and other Edge P0 wiring env vars. Use the two
together; nothing is duplicated.

## Shipped (active in current build)

| Variable | Default | Type | When to set | Doc link |
| --- | --- | --- | --- | --- |
| `CORDUM_EDGE_SHADOW_RETENTION_SHORT` | `168h` (7 days) | Go `time.ParseDuration` string; must be positive | Override terminal TTL for `retention_class=shadow_short` findings (ephemeral CI noise). Zero / negative / unparseable fails Gateway startup. `7d` is **not** supported — use `168h`. | [`shadow-scanner.md` §retention](shadow-scanner.md), `core/edge/shadow/finding_store_redis.go` |
| `CORDUM_EDGE_SHADOW_RETENTION_DEFAULT` | `2160h` (90 days) | Go `time.ParseDuration` string; must be positive | Override terminal TTL for `retention_class=shadow_default` (and the EDGE-141 fallback class). Matches Cordum's audit-event default. | [`shadow-scanner.md` §retention](shadow-scanner.md), `core/edge/shadow/finding_store_redis.go` |
| `CORDUM_EDGE_SHADOW_RETENTION_LONG` | `8760h` (365 days) | Go `time.ParseDuration` string; must be positive | Override terminal TTL for `retention_class=shadow_long` (high-risk findings that must survive an annual audit cycle). | [`shadow-scanner.md` §retention](shadow-scanner.md), `core/edge/shadow/finding_store_redis.go` |
| `CORDUM_EDGE_RUNTIME_INGEST_ENABLED` | unset (treated as disabled) | bool — `true` / `1` / `yes` enable (case-insensitive); anything else disables | Set to `true` to expose `POST /api/v1/edge/runtime/events`. When unset the route returns `503 service_unavailable` with no Redis writes and no SIEM forwarding. | [`runtime-ingestion.md` §disabled-by-default](runtime-ingestion.md), `core/controlplane/gateway/handlers_edge_runtime_ingest.go` |
| `CORDUM_EDGE_RUNTIME_REPLAY_REQUIRED` | unset (treated as required) | bool-ish opt-out — only `false` / `0` / `no` disable the nonce requirement | Transitional non-production compatibility for collectors that cannot yet send the runtime ingest `nonce` field. Production should leave this unset so Redis replay protection is required. | [`runtime-ingestion.md` §wire envelope](runtime-ingestion.md), `core/edge/runtimeingest/adapter.go` |
| `CORDUM_EDGE_SHADOW_SCAN_ENABLED` | unset (treated as disabled) | bool — `true` / `1` / `yes` enable (case-insensitive); anything else disables | Local opt-in for `cordumctl shadow scan`. Without this env var (or the equivalent `--opt-in` flag) the scanner refuses to run. Pair with `CORDUM_EDGE_MANAGED_POLICY_MODE=enterprise-strict` to invariant-enable it under managed settings. | [`shadow-scanner.md`](shadow-scanner.md), `cmd/cordumctl/shadow_scan.go` |
| `CORDUM_EDGE_SHADOW_K8S_ENFORCE` | unset (treated as off) | bool — **RESERVED, not consulted today** | Reserved env-var name for the future K8s shadow-detector enforce-mode hook (admission webhook / `kubectl patch` / similar). The current EDGE-143 design ships zero enforce-mode code; setting this today has no effect. Each enforce action requires its own ADR per [`kubernetes-ci-shadow-detector-design.md`](kubernetes-ci-shadow-detector-design.md) §11.3 + §16 Q5. | [`shadow-scanner.md` §observe-mode enforcement contract](shadow-scanner.md), [`kubernetes-ci-shadow-detector-design.md`](kubernetes-ci-shadow-detector-design.md) |
| `CORDUM_EDGE_MANAGED_POLICY_MODE` | unset | enum — currently the only recognized value is `enterprise-strict` | Enterprise managed-settings invariant. When `enterprise-strict`, hook policy mode is pinned ahead of any local/dev `CORDUM_EDGE_MODE` value, and the managed-settings generator rejects overrides of any `CORDUM_EDGE_MANAGED_*` value. Typically emitted by the managed-settings template, not set by hand. | [`managed-settings-deploy.md`](managed-settings-deploy.md), [`managed-settings-template.md`](managed-settings-template.md), [`shadow-scanner.md` §managed enforcement](shadow-scanner.md) |

## Planned (NOT YET IMPLEMENTED — per governor ruling `comment-a17f4f1c` on `task-de50a293`)

These env vars are part of the binding `§16` resolution callout in
[`kubernetes-ci-shadow-detector-design.md`](kubernetes-ci-shadow-detector-design.md)
but have **no consumer in the current build**. They are reserved for
the named EDGE-143.x follow-up tasks and will move to the **Shipped**
table above when those tasks land. Setting them today has no effect.

| Variable | Future default | Type | Resolves which question | Ships with |
| --- | --- | --- | --- | --- |
| `CORDUM_EDGE_SHADOW_PII_MODE` | `pseudonymize` | enum — `pseudonymize` \| `hash` \| `drop` | Q2: GDPR / UK-DPA processing-record discipline for `principal_id` extracted from `github.actor` and equivalents. `pseudonymize` emits first 3 chars + 8-char hash suffix; `hash` emits the full hash only; `drop` omits the field entirely. | `EDGE-143.4` (network-signal aggregator) — `task-2b0edf73`. |
| `CORDUM_EDGE_SHADOW_OIDC_TRUST_<provider>` | GitHub Actions: `https://token.actions.githubusercontent.com`; GitLab.com SaaS: `https://gitlab.com`; all others: unset (operator-supplied) | URL string, or the literal `disabled` to refuse OIDC for that provider (falls back to the `§6.3` tier-2 path) | Q6: which CI OIDC issuers Cordum trusts by default for `tenant_source=oidc_claim` precedence. `<provider>` is the lowercased provider name (`github`, `gitlab`, `jenkins`, `buildkite`, `circleci`). | `EDGE-143.2` (GitHub Actions detector) — `task-42467eb5`; `EDGE-143.3` (GitLab / Jenkins / Buildkite / CircleCI) — `task-5d8c904c`. |
| `CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_<provider>` | unset (operator picks) | string | Q6: the OIDC `aud` claim Cordum should require for the named provider. Operators must set this to whatever value their workflows already mint; Cordum ships no default audience because it varies per organization. | Same as `CORDUM_EDGE_SHADOW_OIDC_TRUST_<provider>` above. |

## See also

- [`configuration.md`](configuration.md) — Gateway / agentd / heartbeat
  / policy-mode / session env vars.
- [`shadow-scanner.md`](shadow-scanner.md) — local `cordumctl shadow
  scan` operator guide.
- [`runtime-ingestion.md`](runtime-ingestion.md) — runtime-event ingest
  endpoint operator guide.
- [`managed-settings-deploy.md`](managed-settings-deploy.md),
  [`managed-settings-template.md`](managed-settings-template.md) —
  enterprise managed-settings rollout.
- [`kubernetes-ci-shadow-detector-design.md`](kubernetes-ci-shadow-detector-design.md)
  §16 — binding `comment-a17f4f1c` resolutions that drive the
  **Planned** table above.
