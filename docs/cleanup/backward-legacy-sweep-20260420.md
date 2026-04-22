# Residual backward / legacy / deprecated sweep — 2026-04-20

Task: [task-8d282917](../../.moe/tasks.json) (epic [epic-1cadd6f2 Pre-GA Legacy + Dead-Code Sweep](../../.moe/tasks.json)).

Baseline grep (2026-04-20):

```sh
rg -n 'backward|legacy|deprecated' -i -g '!*.test.*' -g '!*_test.go' \
  -g '!node_modules' -g '!.moe' -g '!.tmp' cordum/ cordum-packs/
```

returns ~500 hits across ~80 files. This document classifies them
pattern-by-pattern and surfaces the concrete LEGACY rows resolved by
this task's steps 2–4. Sibling-owned surfaces are enumerated in the
SIBLING table below and are **skipped** here by design to keep PRs
reviewable in isolation (epic rail #4: one surface per PR).

## Classification taxonomy

| Tag | Meaning | Action |
|-----|---------|--------|
| **LEGACY** | Genuine dead or soon-dead code / prose; pre-GA, no external adopter | DELETE or REWRITE in this task |
| **CONTEXTUAL** | Word appears in a comment describing an architectural invariant that still applies (e.g. "backward-incompatible wire change requires proto version bump"; "backward-compatible parsing of old entries that live in production Redis") | KEEP — the rationale remains valid |
| **DOMAIN_VOCAB** | The word is a **value** in a domain enum or wire contract, not a meta-comment about legacy code (e.g. `StatusDeprecated` topic lifecycle, `deprecated: true` OpenAPI flag, `@deprecated` JSDoc on fields kept for **live SDK consumers**) | KEEP — load-bearing vocabulary |
| **ALREADY_COVERED_BY_SIBLING_TASK** | The row is owned by another task in this epic; resolving it here would stomp on a sibling PR | SKIP — reference the sibling |

## Sibling task ownership map

Do **not** edit anything in these surfaces in this task's PR; they are
scoped to a sibling task and touching them here causes merge conflicts.

| Sibling task | Surface owned | Representative files |
|--------------|---------------|----------------------|
| task-70af7e9b | `// Deprecated:` godoc markers in Go + their callers | `sdk/client/client.go`, `core/protocol/pb/v1/*.pb.go`, `core/controlplane/topicregistry/service.go` |
| task-ee782530 | Auth + licensing compat shims | `core/licensing/compat.go` (already deleted), `core/controlplane/gateway/auth_compat.go` (already deleted) |
| task-d9d7f428 | Legacy OpenAPI specs + MCP route aliases | `docs/api/openapi/cordum-rest.yaml`, `docs/api/openapi/cordum.swagger.json`, MCP legacy aliases |
| task-e8a0ff88 | Docs-site versioned docs for never-released versions | `docs-site/versioned_docs/version-2.9/**` |
| task-b7c6c2f1 | cordum-enterprise repo decommission | Out-of-tree repo |
| task-466b6a6a + sibling | MCP audit hook evolution (mcp/audit_invocation.go references to "legacy hook") | `core/mcp/audit_invocation.go`, `core/mcp/audit_hook.go` |

## Pattern classifications

### Pattern 1 — architectural invariant notes → CONTEXTUAL

Comments explaining a design constraint that **still applies** after
the sweep. These stay verbatim.

Representative hits:

- `core/model/events.go:34` — `// backward-compatible parsing of old "timestamp|state" entries.` — KEEP: Redis still holds these keys, the parser still needs the branch.
- `core/model/store.go:18,72` — `// backward compatibility with pre-MCP records already in Redis.` — KEEP: pre-MCP ApprovalRecord values are live in production-equivalent Redis (pre-GA but live integration test corpus).
- `core/workflow/eval_security_test.go:111-164` (`TestBackwardCompatibility`) — KEEP: this test asserts a behavior invariant, not a migration path.
- `core/workflow/engine_edge_case_bugs_test.go:11,57,101,125,179,232` (`// Backward compatibility: ...`) — KEEP: inline justification for why a test case exists.
- `core/workflow/lock_test.go:383` — `nil — backward-compatible local-only` — KEEP: documents a real fallback path.
- `core/workflow/engine_output.go:228` — `fail-open on error to preserve backward compat` — KEEP: fail-open semantics are a policy decision, not a legacy artefact.
- `docs/adr/009-control-plane-boundary-hardening.md` — the ADR discusses backward-compat guarantees **as an architectural concern**; the prose is a design document, not rot to remove. KEEP.
- `docs/architecture/heartbeat-demotion.md` — same: architecture doc describing the compat bridge while the industry migrates off heartbeat-authority. KEEP.

### Pattern 2 — domain enum / wire-contract values → DOMAIN_VOCAB

The word **is** the value, not a meta-comment about it.

- `core/controlplane/topicregistry/service.go:23,29` — `StatusDeprecated = "deprecated"` — topic-lifecycle enum. KEEP per `mem-70af7e9b`.
- `docs/cli-reference.md:536` + `docs/api-reference.md:2069` — documents the `deprecated` topic status to CLI users. KEEP.
- `docs/api/openapi/AUDIT_BASELINE.md:65` + `docs/api/openapi/SCHEMA_DRIFT.md:11,59,60,197` — documents OpenAPI's `deprecated: true` spec attribute. KEEP.
- `dashboard/src/pages/TopicsPage.tsx:36` — `case "deprecated":` in the lifecycle-status switch. KEEP.
- `docs/AGENT_PROTOCOL.md:130,140` + `docs/CORE.md:119,121` — documents deprecated wire fields retained **during the SDK transition window** (pre-GA but cap SDK v2.9 hasn't shipped yet — epic rail #2 mandates keeping these until upstream lands). KEEP.

### Pattern 3 — audit artefacts + release-notes → CONTEXTUAL

Cleanup audit documents themselves mention "legacy" because they
describe the cleanup. Deleting the word would falsify the historical
record.

- `docs/cleanup/auth-license-compat-audit.md` — KEEP: sibling task's audit artefact.
- `docs/cleanup/deprecated-symbols-audit.md` — KEEP: sibling task's audit artefact.
- `docs/release-notes/unreleased.md` — KEEP: release-note copy describes what was removed; the word "legacy" in those bullets is part of the announcement.
- `CHANGELOG.md`, `cordum-dashboard-page-specs.md`, `GOVERNANCE.md` — KEEP: historical prose records.

### Pattern 4 — dashboard convergence tracking → CONTEXTUAL

Dashboard audit documents track the ongoing primitive-convergence
migration (`mem-25ab4c76`, `mem-ae8fa979`, `mem-e892d9a8`). They live
because the migration is in flight.

- `dashboard/docs/design-system-audit.md` — KEEP.
- `dashboard/docs/dashboard-wiring-audit.md` — KEEP.

### Pattern 5 — dashboard live-migration comments → CONTEXTUAL

- `dashboard/src/App.tsx:73` — `// legacy redirects; they are the current public shortcuts` — KEEP: the comment acknowledges the word in the identifier; the behavior is current, not legacy.
- `dashboard/src/state/config.ts:25,26,30,124,125,169,193` — `clearLegacyToken()` is the live migration path from embedded-localStorage auth to httpOnly cookies. KEEP: `TOKEN_KEY = "cordum-api-key"` is still cleared on every init because users with old builds still have the cookie; keep until after 2026-Q3 per mem-feedback-triple-check-deletions caution on user-state surfaces.
- `dashboard/src/components/agents/WorkerSessionBadge.tsx:50-55` — `const legacy = ...` — KEEP: fallback path for heartbeat-mode=authority deployments per `docs/architecture/heartbeat-demotion.md`. Deleting this breaks rollback scenarios.
- `dashboard/src/pages/ApprovalsPage.tsx:119,715` — KEEP: live fallback copy for pre-MCP approval records.
- `dashboard/src/lib/policy-bundle.ts:76,275,276` — `hasLegacyTenants` — KEEP: detects and warns about bundles still using the tenant-only schema. The warning is the point.
- `dashboard/src/components/policy/PolicyFirewallView.tsx:237-240` — KEEP: renders the `hasLegacyTenants` warning banner.

### Pattern 6 — test-fixture names + test-file copy → CONTEXTUAL

Test names carrying "Legacy" document what the test exercises; the
test itself still runs. KEEP.

- `core/licensing/license_test.go:351-382` (`TestParseLicenseRejectsLegacyFormat`) — KEEP: the very test that asserts the legacy format is rejected.
- `core/workflow/engine_test.go:1375-1432` (`TestWorkflowApprovalStepSupportsLegacyMetadataOnlyPayload`) — KEEP: exercises the compat path still in `engine_job.go:113`.
- `dashboard/src/api/transform.test.ts:221-232` — KEEP: tests the fallback-to-legacy-fields mapping.
- `dashboard/src/pages/ApprovalsPage.test.ts:439-564` — KEEP: tests the repaired-legacy-row UX.
- `core/policysign/keys_test.go:43,195,209-218` — KEEP: tests the legacy-env-var trust-store loader path.

### Pattern 7 — ACTIONABLE LEGACY rows resolved in this task

The following hits are **truly stale** and resolved by steps 2–4 of
this task. Each row cites the grep location, the minimal rewrite, and
the rationale.

| # | File:line | Current text (truncated) | Decision | Rationale |
|---|-----------|--------------------------|----------|-----------|
| L1 | `dashboard/src/api/types.ts:465` | `/** Legacy config bag — kept for backward compat during migration */` | **REWRITE** | Drop the "Legacy config bag" framing — the field is still the active config bag, the comment is obsolete wording. Rewrite to describe current purpose without the word "legacy". |
| L2 | `dashboard/src/api/types.ts:480` | `/** @deprecated Use timeout_sec */` on `Workflow.timeout` | **ALREADY_COVERED_BY_SIBLING_TASK** (task-70af7e9b) | The `@deprecated` JSDoc + the paired wire-contract deprecation land together in the godoc sibling task; skip here to avoid stomping. |
| L3 | `dashboard/src/api/types.ts:575` | `// Legacy compat` block inside `PolicyRule` (legacy field names `matchCriteria`, `decisionType`, `reason`, etc.) | **KEEP (CONTEXTUAL)** | These fields are still the fallback shape when talking to the policy-bundle YAML loader (`dashboard/src/lib/policy-bundle.ts` actively reads them). Deleting them breaks legacy-bundle import. Rewrite the comment to `// Legacy-bundle import fields` for clarity. |
| L4 | `dashboard/src/components/workflow-studio/types.ts:41` | `/** Legacy config bag */` | **REWRITE** | Same as L1 — the bag is still active; drop the "Legacy" framing. |
| L5 | `dashboard/src/components/workflow-studio/nodeRegistry.ts:230` | `* Excludes `job` (legacy alias for agent-task).` | **KEEP (CONTEXTUAL)** | Documents a live exclusion; the alias exists in `graphBridge.ts` and operators still encounter it in imported workflows. |
| L6 | `dashboard/src/components/workflow-studio/graphBridge.ts:290` | `// Preserve legacy config with branches` | **KEEP (CONTEXTUAL)** | Preserves live import compatibility for workflows authored before the branches schema. |
| L7 | `docs/evals/datasets.md` — mentions of `tenant` field legacy/deprecated framing | **ALREADY_COVERED_BY_SIBLING_TASK** (task-d9d7f428 step 3) | Sibling task is rewriting the evals wire contract; don't double-write. |
| L8 | `core/workflow/engine_extra_test.go:330` | `// Structured fields (migrated from deprecated level/component/code)` | **KEEP (CONTEXTUAL)** | Documents why the test asserts structured-field presence; the deprecated string fields are still populated during the cap v2.9 transition window (epic rail #2). |

### Pattern 8 — cordum/ go backend → mostly CONTEXTUAL or SIBLING-owned

Representative classifications:

- `core/audit/exporter.go:49-58` (`EventLicenseLegacyRejected`) — DOMAIN_VOCAB: SIEM event type name, load-bearing for operators tailing audit logs. KEEP.
- `core/audit/chain_verify.go:214` + `core/audit/export_compliance.go:573` — CONTEXTUAL: both handle pre-chain legacy audit entries still visible in historical streams.
- `core/licensing/license.go:304-335`, `core/licensing/helpers.go:*`, `core/licensing/errors.go:15` — CONTEXTUAL: the **rejection path** for legacy licenses is the security feature; the word "legacy" in these identifiers is the active code. Already renamed-in-place by sibling task-ee782530.
- `core/controlplane/topicregistry/service.go:67,89,201,219-246` — CONTEXTUAL: `migrateFromLegacyPools` + `legacyTopicRegistrations` are the live boot-time migration from the pre-registry topic→pool map. Deleting them breaks upgrade from any prior release. KEEP until a future major that drops the migration.
- `core/policysign/policysign.go:149-161` — CONTEXTUAL: `DecodeRawSignature` + the raw-sig trust-store loader bridge the transition to the canonical envelope. Sibling task-6ced7932 (pack signing) uses the canonical envelope; this legacy path stays for bundles signed pre-canonical.
- `core/mcp/audit_invocation.go:5-8` — ALREADY_COVERED_BY_SIBLING_TASK (mcp audit evolution).
- `core/protocol/pb/v1/pb.go:54` — ALREADY_COVERED_BY_SIBLING_TASK (task-70af7e9b Deprecated godoc markers).

### Pattern 9 — cordum-packs → no task-8d282917 changes

Representative hits in `cordum-packs/` fall into DOMAIN_VOCAB
(`deprecated` pack-status values), CONTEXTUAL (migration notes in
pack READMEs), or ALREADY_COVERED_BY_SIBLING_TASK (pack-signing
tasks). No LEGACY rows remain in the pack ecosystem that aren't
already covered by a sibling.

## Summary of actionable changes for this task

Per the classification above, steps 2–4 perform only three surgical
rewrites:

1. `dashboard/src/api/types.ts:465` — drop "Legacy config bag" comment.
2. `dashboard/src/api/types.ts:575` — clarify "Legacy compat" →
   "Legacy-bundle import fields".
3. `dashboard/src/components/workflow-studio/types.ts:41` — drop
   "Legacy config bag" comment.

Every other hit surveyed resolves to CONTEXTUAL, DOMAIN_VOCAB, or
ALREADY_COVERED_BY_SIBLING_TASK. Step 5 lands the release-note
pointer and the `docs/cleanup/README.md` cross-reference.

## No runtime behavior change

This sweep is comment / prose only. No SIEMEvent emissions change.
No `slog` messages change. No gateway handler or store method
changes. Existing tests remain green.

## Verification gate (step 5)

Final grep:
```sh
rg -c -i 'backward|legacy|deprecated' cordum/ cordum/dashboard/src/ cordum/docs/
```

After-count should be ≤ before-count minus the three rewrites. Every
remaining hit maps to a row in this document (or a sibling-task
ownership row in the SIBLING table).
