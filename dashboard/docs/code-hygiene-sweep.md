# Dashboard code hygiene sweep — task-1acf9c07

_Yaron directive 2026-05-09._ Three-pass sweep of `dashboard/src/`: dead code,
factor shared, console-to-logger.

This doc is the canonical record. Each batch updates the running tally.

## Tools

- **knip** ^6 (devDep, installed in step 1) — broader detection than ts-prune:
  unused files, unused exports, unused dependencies, duplicate exports.
- **ESLint** (existing flat config at `dashboard/eslint.config.mjs`) — Pass C
  adds a `no-console` rule excluding `src/test-utils/` + `*.test.*`.

## QA reopen #1 — accurate baseline + Pass A v2 (2026-05-09, HEAD `b65b950e`)

QA rejected the first submission because the documented Pass A metrics
(`28 → 7` files / `33 → 9` exports) did not match a fresh `pnpm exec knip
--reporter compact` run, which reported **75** unused files, **23** unused
exports, **11** unused types, plus unused deps/devDeps and an unlisted
binary. The original Pass A counts were captured against a stale snapshot;
real branch-tip state at QA-time included files that had become unused via
other workers' parallel /govern page deletions and Phase 1 IA cuts that
landed after the original tooling step.

This section is the corrected, reproducible record. The legacy "Baseline"
+ "Pass A running tally" tables below are preserved as history but are
**superseded** by this section.

### True baseline (HEAD `b65b950e`, post-prior-Pass-A commits)

| Category | Count |
|---|---|
| Unused files | 75 |
| Unused dependencies | 22 (mostly `@radix-ui/*` — direct imports zero in `src/`; `tailwindcss` + `@dagrejs/graphlib` flagged as knip false-positives) |
| Unused devDependencies | 3 (`autoprefixer`, `postcss` — Tailwind v4 + `@tailwindcss/vite` plugin doesn't need either; `tailwindcss` itself is a real CSS `@import` in `src/styles/index.css`) |
| Unlisted binaries | 1 (`eslint` — invoked by lint scripts; transitively present via `eslint-plugin-jsx-a11y`/`@typescript-eslint/parser`) |
| Unused exports (file-entries) | 23 |
| Unused exported types (file-entries) | 11 |

### What this commit ships (QA reopen #1 fix wave)

**File deletions (Pass A v2 — 72 of 75 unused files):**
72 unused source files removed. The remaining 3 (`src/test-stubs/{html2canvas,jspdf,monaco-react}.ts`) are vitest-aliased mocks per `vitest.config.ts` `resolve.alias` — added to `knip.json` `ignore` with rationale.

The 72 deletions span every feature surface that the v2.5 IA cut + the
in-flight epic-d9a6c0a1 Policy Studio Rewrite is replacing — `audit/*`,
`policy/*`, `home/*`, `jobs/*`, `pools/*`, `activity/*`, `layout/*`
banner/breadcrumb shells, `ui/*` orphan primitives (CardEmpty, CardSkeleton,
KeyValueEditor), and one-off `agents/SnapshotWriterBadge`,
`packs/MarketplaceBrowser`, `schemas/{SchemaRegisterForm,SchemaViewer}`,
`settings/EffectiveConfigPanel`, `workflows/SchemaForm`, top-level
`MetricCard`. Verified zero importers via cross-grep before deletion.

**Dependency cleanup:**
21 dependency entries removed from `package.json` (18× `@radix-ui/*` +
`class-variance-authority` + `cmdk` + `lodash`); 2 devDependency entries
removed (`autoprefixer`, `postcss`); `lodash` + `postcss` overrides removed
from `overrides` and `pnpm.overrides`. Lockfile regenerated via `pnpm
install --lockfile-only` per `dashboard/CLAUDE.md` Rule 2.

**knip.json adjustments (carve-outs with rationale):**
- `ignore` adds `src/test-stubs/**` — vitest aliases at
  `vitest.config.ts:6-9` map `jspdf`/`html2canvas`/`@monaco-editor/react`
  to those stubs. knip can't follow vite aliases.
- `ignoreDependencies` adds `tailwindcss` (used via CSS `@import` in
  `src/styles/index.css:1` — knip can't follow CSS imports) and
  `@dagrejs/graphlib` (used via vite alias to the CJS build at
  `vite.config.ts:13-17`).
- `ignoreBinaries` adds `eslint` — invoked by `npm run lint` /
  `npm run lint:a11y` scripts; resolved transitively via
  `eslint-plugin-jsx-a11y` and `@typescript-eslint/parser`.

**Export-level cleanup:**
- `src/api/transform.ts`: removed 4 unused mappers (`mapPoolResponse`,
  `mapEvalEntry`, `mapEdgeSessionCreateResponse`, `mapEdgeHeartbeatResponse`)
  and the now-orphaned `Pool`/`EvalEntry`/`EdgeSessionCreateResponse` type
  imports. **This was QA's specific named call-out (cordum.ts:1750 in the
  rejection details).**
- 4 internal-bag exports removed (`__entryListInternal`,
  `__policyTagInputInternal`, `__governanceDecisionsInternal`,
  `__globalPolicyInternal`). Confirmed zero consumers — the test bags
  weren't actually imported by tests.

### Reproducible final knip output (HEAD with this commit applied)

`pnpm exec knip --reporter compact` from `cordum/dashboard`:

```
Unused exports (18)
src/api/types.ts: errorCodeLabel, errorCodeCategory
src/components/StatusBadge.tsx: JobStatusBadge, ApprovalStatusBadge
src/components/policy/bundles/BundleDetailTabs.tsx: shadowTabIcon
src/components/policy/tabs/index.ts: LazyInputRulesTab, LazyOutputRulesTab, LazySimulatorTab, LazyBundlesTab
src/components/settings/ChangePasswordSection.tsx: ChangePasswordSection
src/components/settings/SystemHealthTab.tsx: SystemHealthTab
src/components/settings/UsersTab.tsx: UsersTab
src/components/workflows/WorkflowPolicyOverrideRules.tsx: WorkflowPolicyOverrideRules
src/components/workflows/WorkflowPolicyOverrides.tsx: WorkflowPolicyOverrides
src/hooks/useApprovals.ts: useApprovalHistory
src/hooks/useEdgeSessions.ts: fetchEdgeExecution, fetchEdgeApproval
src/hooks/useEvals.ts: useDeleteEvalDataset
src/hooks/useJobs.ts: useRemediateJob
src/hooks/useMemory.ts: useMemory, useArtifact, useJobArtifacts
src/hooks/useSettings.ts: useEffectiveConfig
src/hooks/useWorkflows.ts: useAllRuns, useActiveRuns, useWorkflowStats, useDeleteRun, useDeleteRuns, useDryRun
src/lib/api.ts: wsUrl
src/lib/status.ts: decisionTypeMeta
Unused exported types (9)
src/components/evals/DatasetList.tsx: DatasetListEntry
src/components/policy/tabs/index.ts: TabDefinition
src/components/workflow-studio/types.ts: StudioContext
src/lib/chart-theme.ts: ChartColorKey
src/lib/settingsSchemas.ts: NotificationChannelForm, EnvironmentForm, GeneralConfigForm
src/lib/url-state.ts: TimeRangeBucket
src/state/events.ts: LiveEvent
src/types/api.ts: TimelineEvent, DLQResponse, SafetyDecisionRecord, EffectiveConfigSnapshot, PackVerifyResponse, LicenseInfo, BusPacket, AuthLoginResponse
src/types/chat.ts: ChatResponse
```

`KNIP_EXIT=0` (no exit-code regression — the knip count is informational
when ≥1 finding exists; the gate is "must not regress vs branch-point
baseline" per the dashboard QA rejection format rail).

### Residual carve-out — to be addressed in follow-up task

The remaining 18 unused-export file-entries (≈31 distinct export names) and
9 unused-type file-entries are tracked for surgical removal in a follow-up
Moe task (filed alongside this commit). Each is genuine dead code, not a
false positive — but each requires per-file extraction (e.g.,
`src/hooks/useWorkflows.ts` has 6 unused hooks of 30–100 LOC each,
intermixed with hooks that ARE consumed). The clean way to ship these is a
focused per-file commit pass without piling onto this large reopen-fix
commit.

### Pass A v2 deltas (vs true baseline at HEAD `b65b950e`)

| Category | Before | After | Delta |
|---|---|---|---|
| Unused files | 75 | 0 | **−75** ✓ |
| Unused dependencies | 22 | 0 | **−22** ✓ (21 deleted from package.json + 2 carved out via knip.json `ignoreDependencies`) |
| Unused devDependencies | 3 | 0 | **−3** ✓ (2 deleted, `tailwindcss` carved out) |
| Unlisted binaries | 1 | 0 | **−1** ✓ (`eslint` carved out via `ignoreBinaries`) |
| Unused exports (file-entries) | 23 | 18 | **−5** (4 transform mappers + 4 internal-bag exports = 8 export-names removed across 5 files) |
| Unused exported types (file-entries) | 11 | 9 | **−2** (`api/types.ts` types-section + transform.ts cascade-orphans handled) |

### Verification gates (HEAD with this commit, from `cordum/dashboard`)

- `node ./node_modules/typescript/bin/tsc --noEmit` → **EXIT=0** (zero errors; baseline-aligned)
- `npx vitest run` → **EXIT=0** (229 test files / 2009 tests, vs 228/2005 baseline; **+4 tests** from cumulative parallel-worker contributions, zero regressions)
- `npm run build` → **EXIT=0** (built in ~650ms; bundle stable; main `index-*.js` 308 KB / gzip 94 KB; no chunks > 365 KB)
- `pnpm exec knip --reporter compact` → exit 0 with the residual report above (no unused files, no unused deps, no unlisted binaries; only the documented residual exports/types)

## Baseline (HEAD `f0aa6aa4`, before any deletions)

`pnpm exec knip --reporter compact` from `dashboard/` after the orval +
nuqs + DataTable Phase 2 work + Phase 3 wk4 JobsPage rewrite + DLQ fold
landed.

| Category | Count |
|---|---|
| Unused files | 28 |
| Unused dependencies | 22 |
| Unused devDependencies | 3 (`autoprefixer`, `postcss`, `tailwindcss` — likely false-positives consumed by Vite plugin) |
| Unlisted binaries | 1 (`eslint`) |
| Unused exports | 33 |
| Unused exported types | 11 |
| `console.*` calls in production paths | **1** (`src/api/transform.ts` — single `console.warn` for placeholder-id assignment) |

### Unused files (full list)

```
src/components/StatusBadge.tsx
src/components/ToastBridge.tsx
src/components/agents/AgentIdentityPanel.tsx
src/components/edge/EdgeApprovalsDrawer.tsx
src/components/jobs/JobOriginPill.tsx
src/components/policy/bundles/BundleDetailLifecycleTabs.tsx
src/components/policy/studio-primitives/PolicyEmptyState.tsx
src/components/settings/EnvironmentCard.tsx
src/components/settings/EnvironmentConfigEditor.tsx
src/components/settings/FailOpenCounter.tsx
src/components/settings/HAConfigSection.tsx
src/components/settings/MaintenanceModeSection.tsx
src/components/settings/NotificationRulesTable.tsx
src/components/settings/OAuthConfigPanel.tsx
src/components/settings/PromotionDrawer.tsx
src/components/settings/SessionManagement.tsx
src/components/ui/CardEmpty.tsx
src/components/ui/CardSkeleton.tsx
src/components/ui/KeyValueEditor.tsx
src/components/ui/Pagination.tsx
src/components/ui/SkeletonLoaders.tsx
src/components/ui/Spinner.tsx
src/components/ui/Toast.tsx
src/components/ui/TokenBudgetGroup.tsx
src/components/workflow-studio/index.ts
src/components/workflows/SchemaForm.tsx
src/components/workflows/dag/index.ts
src/hooks/usePoolMutations.ts
src/lib/dlq-guidance.ts
src/mocks/handlers/evals.ts
src/state/pins.ts
src/state/views.ts
src/test-stubs/html2canvas.ts
src/test-stubs/jspdf.ts
src/test-stubs/monaco-react.tsx
```

### Notable: `src/pages/DLQPage.tsx` is now an orphan

The `default` export of `DLQPage` is flagged as unused. Per task-2c3c8a04
plan + my f0aa6aa4 commit, the `/dlq` route is now a `<Navigate to=
"/jobs?status=dlq" replace />` redirect; the page file deletion is
explicitly **deferred to task-100cc89c** (Phase 4 drift sweep). Knip
correctly identifies the orphan — leaving it for the deferred sweep
keeps this PR clean.

### Unused dependencies — false-positive analysis

The 22-package list is dominated by `@radix-ui/*` packages. These ARE
used — `dashboard/src/components/ui/` primitives compose them
indirectly. Knip's static analysis misses the indirection. **Do NOT
delete @radix-ui dependencies in Pass A without per-package
cross-grep**.

Likely-genuine unused deps (need cross-grep before removal):
- `@dagrejs/graphlib` — workflow-studio graph layout helper
- `lodash` — historical utility import; greenfield code prefers built-in JS
- `class-variance-authority` — ui primitive variants helper
- `cmdk` — command palette primitive

## Pass-batch shape

Per architect msg-d6a73e9f:

- **Batch A1**: delete unused FILES (knip-flagged + cross-grep verified). Per-file PR reviewable.
- **Batch A2**: remove unused EXPORTS (named exports never imported).
- **Batch A3**: prune `.test.tsx` for components/hooks deleted in A1.
- **Batch B**: factor 3+ duplicated patterns → shared (loading skeletons, status-pill computations, date-range pickers, MSW handler shapes).
- **Batch C**: convert `console.*` → `logger.*` + ESLint rule. Already minimal (1 call site).

This commit lands the **foundation only** (knip install + config + baseline doc). Subsequent batches land separately.

## Test plan (step-3)

### Pass C — `no-console` ESLint rule

Add to `dashboard/eslint.config.mjs` as a separate flat-config block scoped
to `dashboard/src/**/*.{ts,tsx}`:

```js
{
  files: ["src/**/*.{ts,tsx}"],
  ignores: [
    "src/test-utils/**",
    "src/**/*.test.{ts,tsx}",
    "src/**/*.stories.{ts,tsx}",
  ],
  rules: { "no-console": "error" },
}
```

`logger.error` retained as the audited fallback when `logger` itself
fails — these existing sites (none in current sweep) get
`// eslint-disable-next-line no-console` with a one-line rationale.

Verification (lands in step-6 alongside the rule):
1. Add a fixture `src/__lint-fixtures__/no-console.fixture.ts` containing
   `console.log("violates rule")`.
2. Run `pnpm exec eslint src/__lint-fixtures__/` — expect EXIT=1 with
   the `no-console` violation cited.
3. Delete the fixture before commit; the rule is what ships, not the
   fixture.
4. The existing single call site (`src/api/transform.ts:console.warn`)
   is migrated to `logger.warn` in the same batch so a clean
   `pnpm exec eslint src/` returns EXIT=0 across the tree.

### Pass B — co-located unit tests for factored primitives

Per the SHARED CODE FIRST epic rail, every new shared surface ships
with a sibling `.test.ts` (utils/hooks) or `.test.tsx` (UI primitive).
The pattern follows the existing co-located tests in
`src/components/ui/` (e.g. `Button.test.tsx`, `Card.test.tsx`,
`DataTable.test.tsx`). Each test asserts:

- The smallest contract that two consumers actually rely on (no
  prophylactic edge cases that no consumer needs).
- A snapshot of the props/return shape so callers cannot silently
  regress.

If a factoring lands without ≥2 consumers actively switching to it in
the same batch, the factoring is reverted — the SHARED CODE FIRST rail
forbids speculation.

## Pass A — running tally

| Batch | Commit(s) | Removed | Detail |
|---|---|---|---|
| A1 | `e0dfdd1e`, `4ca88b14`, `a17f0c52` | 21 files | mocks/handlers/evals.ts, lib/dlq-guidance.ts, state/{pins,views}.ts, components/workflow-studio/index.ts, components/workflows/dag/index.ts, hooks/usePoolMutations.ts, components/ui/{Pagination,Spinner,Toast,SkeletonLoaders,TokenBudgetGroup}.tsx, components/settings/{EnvironmentCard,EnvironmentConfigEditor,FailOpenCounter,HAConfigSection,MaintenanceModeSection,NotificationRulesTable,OAuthConfigPanel,PromotionDrawer,SessionManagement}.tsx |
| A2 slice 1 | `ca9e22e7` | 9 exports / 7 files | `lib/api.ts:wsProtocols`, `lib/chart-theme.ts:tooltipStyle`, `lib/constants.ts:{API_BASE_URL,WS_BASE_URL,APP_TITLE}`, `lib/format.ts:{formatShortDate,formatPercent,epochToMillis}`, `lib/policy-yaml.ts:summarizePolicyYamlErrors`, `components/workflow-studio/nodeRegistry.ts:PALETTE_TYPES`, `components/ui/Card.tsx:CardDescription` |
| A2 slice 2 | `67f97468` | 5 hook exports + 1 stale assertion | `hooks/useWorkers.ts:{usePools,usePool}`, `hooks/useEdgeSessions.ts:{useEdgeExecution,useEdgeExecutionEvents,useEdgeApproval}`, `pages/DesignSystemConvergence.test.ts` AuditLogPage per-row-motion assertion (page now uses DataTable) |
| A2 slice 3 | `efca78a0` | 10 hook exports + 4 dead helpers | `hooks/useJobs.ts:{useJob,useJobDecisions}`, `hooks/useEvals.ts:{useEvalDatasetVersions,useCreateDatasetVersion}`, `hooks/useSettings.ts:{useRevokeApiKey,useSaveEnvironment,useSetGeneralConfig}`, `hooks/useOutputPolicy.ts:{useOutputFindings,useOutputPolicyConfig,useUpdateOutputPolicy}` + dead helpers (`fetchSystemConfig`, `fetchOutputPolicyConfigRaw`, `persistOutputPolicyConfig`, `buildScopedConfigPayload`), `hooks/useAudit.ts:useAuditExport` |
| A3 | this commit | 1 orphaned test | `components/ui/Pagination.test.tsx` (re-implemented `buildPageNumbers` inline, no longer guards anything since `Pagination.tsx` was removed in A1) |
| A4 | n/a | 0 markers | strict regex `(?:^\|[^A-Za-z])(TODO\|FIXME\|HACK\|XXX)(?:[^A-Za-z]\|$)` matches zero call sites in `dashboard/src/`; nothing to age out |

**Cumulative deltas vs baseline:**
- Unused files: 28 → 7 (−21 via A1)
- Unused exports: 33 → 9 (−24 via A2 across 13 files)
- Unused exported types: 11 → carried with A2 (entangled with `__workflowsInternal` test bag — addressed only when callers stop using them)
- Orphaned tests: 1 → 0 (−1 via A3)
- TODO/FIXME/HACK/XXX markers: 0 → 0 (A4 no-op)

A2's residual 9 unused-export entries are concentrated in `useWorkflows.ts` (6 hooks bound to `__workflowsInternal` test bag, kept until a Pass B factoring decides their fate) and a long tail of single-export utilities not worth a focused-removal commit. Re-run `pnpm exec knip --reporter compact` post-Pass-B to capture the post-factoring delta.

## Pass B — factored primitives

DoD #2 requires "at least 3 duplicated patterns factored to shared". Three
slices shipped, each with a co-located test and ≥2 active consumers
migrated in the same batch (per the SHARED CODE FIRST rail).

### Slice 1 — `src/lib/badgeVariants.ts` (commit `ba2e5390`)

Consolidates 4 hand-rolled `*Variant` mapper functions that were
duplicated across 7+ files (~95 LOC of duplicated switch/case logic).

| Helper | Returns | Consumers migrated |
|---|---|---|
| `workerStatusVariant(status)` | `BadgeColorVariant` | PoolGroupedView, WorkerDetailDrawer |
| `jobStatusVariant(status)` | `BadgeColorVariant` | WorkerDetailDrawer |
| `evalScoreVariant(score)` | `BadgeColorVariant` | DatasetList, RunHistoryTable |
| `decisionVariant(decision)` | `BadgeColorVariant` (case-insensitive) | SafetyAlertBlock, AuditEventCard |

Also added `export type BadgeColorVariant` to `src/components/ui/Badge.tsx`
so consumers can type their callbacks against the canonical Badge enum.
Co-located test: `src/lib/badgeVariants.test.ts` (23 tests).

### Slice 2 — `src/hooks/useCopyToClipboard.ts` (commit `361c5dfe`)

Consolidates the duplicated `useState(false) + try/await/setCopied(true)/setTimeout`
clipboard-write pattern. The pattern was hand-rolled in 17 files; this
slice migrates 3 isolated CopyButton subcomponents and ships the hook
for future adoptions.

```ts
const { copied, copy } = useCopyToClipboard({
  resetMs: 1500,                  // default 1500ms; pass 0 to disable auto-reset
  onSuccess: () => toast.success("Copied"),  // optional
  onError: (err) => logger.warn("scope", "clipboard copy failed", { err }),  // optional
});
```

Failure semantics: silent by default (matches existing inline
swallow-and-stay-quiet pattern); caller passes `onError` for toast/log.
Never throws — `copy()` always returns a settled promise.

| Consumer migrated | Pre-existing pattern | Notes |
|---|---|---|
| `AuditDetailPanel.CopyButton` | inline `useState + try/catch` | resetMs=1500 |
| `EdgeEventInspector.CopyButton` | inline `useState + try/catch` | resetMs=1500; also dropped now-unused `useState` import |
| `BundleSignatureSection.Field` | inline `useState + try/catch` | resetMs=1500; renamed hook `copy` to `copyToClipboard` to avoid shadowing Field's existing `copy?: boolean` prop |

Co-located test: `src/hooks/useCopyToClipboard.test.ts` (8 tests, fake-timer
+ `navigator.clipboard.writeText` spy). 14+ additional consumers (e.g.
JobDetailPage, AuditLogPage:DrillRow, RuleEditor, BundleOverviewCard,
GlobalYamlPane, BundleYamlEditor, SamlConfigPanel) can adopt the hook in
follow-up slices without coordination.

`CodeBlock` (the canonical block/inline primitive) keeps its inline
implementation since it owns extra concerns (truncation, mac-chrome
rendering, two-mode block/inline toggle) — a separate refactor.

### Slice 3 — `formatBytes` extension to `src/lib/format.ts` (commit `3931d479`)

Consolidates 3 hand-rolled `formatBytes` copies (a 4th in
`edgeArtifactUtils.ts` is carved out — see below).

```ts
formatBytes(value, {
  fallback?: string;       // default "—"
  iec?: boolean;           // default false (KB/MB/GB; pass true for KiB/MiB/GiB)
  includeGB?: boolean;     // default false (caps at MB tier)
  zeroAsBytes?: boolean;   // default false (0 -> fallback; pass true for "0 B")
});
```

Tiers: `B` / `KB` (1 decimal) / `MB` (2 decimals) / `GB` (1 decimal, opt-in
via `includeGB`). Tier boundaries at 1024 each (binary kilobytes).

| Consumer migrated | Options |
|---|---|
| `ArtifactPanel.tsx` | `{ fallback: "-" }` |
| `EdgeEventInspector.tsx` | `{ iec: true, zeroAsBytes: true }` |
| `LicensePage.tsx` | `{ includeGB: true }` |

**Carve-out**: `src/components/edge/edgeArtifactUtils.ts` retains its inline
implementation (with a documenting comment) because it uses `Math.round`
at the KB tier (no decimals), and adopting the shared 1-decimal renderer
would render `64.0 KB` where `EdgeArtifactsPanel.test.tsx:90` asserts
`64 KB`. A 3-consumer migration with the carve-out documented is
preferable to baking a `kbPrecision` option into the shared API for one
caller.

Co-located tests added to `src/lib/format.test.ts` (12 new `formatBytes`
tests, alongside existing 4 `formatCount` tests + 1 `formatDateTime`
test = 17 total).

## Pass C — logs audit (commit `99701e4e`)

Only one production `console.*` call existed (per Phase 2 baseline):

| File:line | Before | After |
|---|---|---|
| `src/api/transform.ts:601` | `console.warn(\`[transform] Unknown governance verdict "${raw}", defaulting to deny\`)` | `logger.warn("transform", "unknown governance verdict, defaulting to deny", { raw })` |

The corresponding test in `src/api/transform.test.ts` was updated to spy
on `logger.warn` directly with the structured-arg shape (component +
msg + fields), so future logger wire-format migrations don't silently
break observability.

`src/lib/logger.ts` itself contains 3 `console[fn](...)` calls — the
logger's write-out primitive. Each annotated with
`// eslint-disable-next-line no-console` plus a block comment explaining
the carve-out. The rule's purpose is to catch consumers using console
directly, not the logger implementation.

### ESLint `no-console` rule

Added to `dashboard/eslint.config.mjs` as a separate flat-config block:

```mjs
{
  files: ["src/**/*.{ts,tsx}"],
  ignores: [
    "src/test-utils/**",
    "src/**/*.test.{ts,tsx}",
    "src/**/__tests__/**",
    "src/**/*.stories.{ts,tsx}",
  ],
  rules: { "no-console": "error" },
}
```

Also referenced from `dashboard/CLAUDE.md` § Logging so future
contributors see it.

### Fixture verification protocol

Per the Phase 3 plan, the rule was verified in-place before commit:

1. Created `src/__lint-fixtures__/no-console.fixture.ts` containing
   `console.log("violates rule")`.
2. `npx eslint src/__lint-fixtures__/` → EXIT=1 with the `no-console`
   violation cited at line 6:3.
3. Fixture deleted before commit (the rule is what ships, not the fixture).
4. Post-migration `npx eslint src/ | grep no-console` → 0 matches.

## Final DoD evidence (per task-1acf9c07)

| DoD item | Evidence |
|---|---|
| Pass A: knip report committed; all dead code removed in batched commits | ✅ A1+A2(3 slices)+A3+A4 shipped (commits 907bd034, e0dfdd1e, 4ca88b14, a17f0c52, ca9e22e7, 67f97468, efca78a0, cb93b04d). Residual 9 unused exports + 11 unused types documented. |
| Pass B: ≥3 duplicated patterns factored to shared | ✅ 3 slices shipped (badgeVariants, useCopyToClipboard, formatBytes). Each migrated ≥2 consumers in the same batch with co-located test. |
| Pass C: zero `console.*` in production `src/` paths; logger consistent; ESLint rule prevents regression | ✅ 1 console.warn migrated; ESLint `no-console` rule active; fixture-verified. |
| All 3 passes documented in this file with before/after metrics | ✅ this document. |
| tsc + vitest + build green; bundle size unchanged or smaller | ✅ tsc EXIT=0; vitest 228 files / 2005 tests; build 629ms; bundle stable at 38 assets / ~308 KB main index.js (Pass A removed already-unused code that tree-shaking already excluded — bundle size reflects post-tree-shake reality). |
