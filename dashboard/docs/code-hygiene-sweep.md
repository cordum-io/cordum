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

### Reopen #2 final knip closure (current HEAD)

QA rejected reopen #1 because a fresh `pnpm exec knip --reporter compact`
still exited 1 with **18 unused-export file entries** and **11 unused
exported-type file entries**. The current fix does not carve those out or
defer them — it removes the residual findings.

`pnpm exec knip --reporter compact` from `cordum/dashboard` now emits no
findings and exits cleanly:

```text
<no output>
```

`KNIP_EXIT=0` (reproduced 2026-05-09 after the reopen #2 cleanup).

### Reopen #2 cleanup details

- Removed the obsolete public helpers called out by QA: `errorCodeLabel`,
  `errorCodeCategory`, `JobStatusBadge`, `ApprovalStatusBadge`,
  `shadowTabIcon`, `wsUrl`, `decisionTypeMeta`, and the unused Edge
  detail-fetch helpers.
- Deleted dead hook surfaces with no production consumers:
  `useMemory.ts`, the unused workflow-run hooks, `useApprovalHistory`,
  `useDeleteEvalDataset`, `useRemediateJob`, and `useEffectiveConfig`.
- Deleted vestigial settings/workflow/policy tab components that were kept
  alive only by stale tests: old settings tabs/panels, workflow policy
  override UI, legacy policy tab wrappers, and their orphan tests.
- Removed stale exported model types from `api/types.ts`, `types/api.ts`,
  `types/chat.ts`, `state/events.ts`, `chart-theme.ts`, `settingsSchemas.ts`,
  `url-state.ts`, and `workflow-studio/types.ts`.
- Kept the existing `knip.json` false-positive carve-outs only for toolchain
  realities already documented in reopen #1 (`src/test-stubs/**`, CSS/Vite
  alias dependencies, and the `eslint` binary). No residual code export was
  hidden by config.

### Pass A v2/final deltas (vs true baseline at HEAD `b65b950e`)

| Category | Before | After | Delta |
|---|---|---|---|
| Unused files | 75 | 0 | **-75** ✓ |
| Unused dependencies | 22 | 0 | **-22** ✓ (21 deleted from package.json + 2 carved out via knip.json `ignoreDependencies`) |
| Unused devDependencies | 3 | 0 | **-3** ✓ (2 deleted, `tailwindcss` carved out) |
| Unlisted binaries | 1 | 0 | **-1** ✓ (`eslint` carved out via `ignoreBinaries`) |
| Unused exports (file-entries) | 23 | 0 | **-23** ✓ |
| Unused exported types (file-entries) | 11 | 0 | **-11** ✓ |

### Verification gates (current HEAD, from `cordum/dashboard`)

- `pnpm exec knip --reporter compact` → **EXIT=0** (no output)
- `node ./node_modules/typescript/bin/tsc --noEmit` → **EXIT=0**
- `npx vitest run` → **EXIT=0** (237 files / 1964 tests; clean tree with unrelated Dashboard 6 work temporarily stashed and restored)
- `npm run build` → **EXIT=0** (built in 5.53s; initial `index-*.js` 317.43 KB raw / 96.45 KB gzip, still under the 400 KB / 120 KB soft thresholds)

## Pass A v3 residual export/type confirmation — task-ec7bcb78 (2026-05-09)

This follow-up rechecked the residual unused-export and unused-exported-type
list called out in the reopen #1 fix wave. At branch-tip validation time the
listed residuals were already absent from the target files, so this pass did
not delete code. The reproducible final knip report remains clean:

```text
<no output>
```

`pnpm exec knip --reporter compact` from `cordum/dashboard` → **EXIT=0**.

### task-ec7bcb78 group audit

| Group | Result |
|---|---|
| Scalar exports (`errorCodeLabel`, `errorCodeCategory`, `wsUrl`, `decisionTypeMeta`, `shadowTabIcon`) | No target exported dead symbols remained. Only a live local `wsUrl` helper exists in `src/hooks/useEventStream.ts`. |
| Component/export barrels (`JobStatusBadge`, `ApprovalStatusBadge`, Lazy* tabs, settings/workflow named exports) | No target exported dead symbols remained. Lazy* matches are live local constants in `src/pages/govern/PolicyOverviewPage.tsx`. |
| Hook group A (`useMemory`, artifact hooks, workflow run/delete/dry-run hooks) | No exact target hook definitions or importers remained. |
| Hook group B (`useApprovalHistory`, Edge detail fetchers, eval/job/settings hooks) | No exact target hook definitions or importers remained. Generated API hook names under `src/api/generated/**` were left untouched. |
| Type-only residuals | No target exported dead types remained. Remaining exact-name matches are live/non-target local or consumed types (`LicenseInfo`, local `BusPacket`, local `DLQResponse`). |

No retained item is a knip residual finding; the remaining exact-name matches
above are live code or generated code outside this task's deletion scope.

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

**Cumulative deltas vs original baseline (after reopen #2):**
- Unused files: 28 → 0 (initial A1 plus reopen cleanup)
- Unused exports: 33 → 0 (A2 plus reopen cleanup)
- Unused exported types: 11 → 0 (reopen cleanup)
- Orphaned tests: 1 → 0 (A3 plus stale-test cleanup)
- TODO/FIXME/HACK/XXX markers: 0 → 0 (A4 no-op)

The older A2 residual-export note is superseded by the reopen #2 closure
section above: current `pnpm exec knip --reporter compact` exits 0 with no
findings.

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
| Pass A: knip report committed; all dead code removed in batched commits | ✅ A1+A2(3 slices)+A3+A4 plus reopen #1/#2 cleanup. Current `pnpm exec knip --reporter compact` emits no findings and exits 0. |
| Pass B: ≥3 duplicated patterns factored to shared | ✅ 3 slices shipped (badgeVariants, useCopyToClipboard, formatBytes). Each migrated ≥2 consumers in the same batch with co-located test. |
| Pass C: zero `console.*` in production `src/` paths; logger consistent; ESLint rule prevents regression | ✅ 1 console.warn migrated; ESLint `no-console` rule active; fixture-verified. |
| All 3 passes documented in this file with before/after metrics | ✅ this document, including reopen #2 final knip closure. |
| tsc + vitest + build green; bundle size unchanged or smaller | ✅ tsc EXIT=0; vitest EXIT=0 (237 files / 1964 tests); build EXIT=0 (5.53s; initial bundle 317.43 KB raw / 96.45 KB gzip, under soft thresholds). |

## Bundle-size baseline (Phase 5d, task-50bbfd7d, 2026-05-09)

`pnpm run build` now emits `dist/stats.html` via `rollup-plugin-visualizer`,
and `scripts/parse-bundle-stats.mjs` posts a per-chunk size table on every
PR (CI workflow `ci.yml` `dashboard-test` job). Soft thresholds are
warn-only — the parser always exits 0; warnings surface as `::warning::`
lines in the workflow run UI and as a `⚠ Soft-threshold warnings` section
in the PR comment.

**Baseline captured 2026-05-09 (dashboard branch HEAD)**

| Bucket | Raw | Gzip | Brotli |
| --- | ---: | ---: | ---: |
| Initial (`index-*.js`) | 305.4 KB | 92.2 KB | 79.8 KB |
| Total (183 chunks) | 2532.7 KB | 759.3 KB | _n/a_ |

**Top 5 route chunks by raw size**

| Chunk | Raw | Gzip |
| --- | ---: | ---: |
| `generateCategoricalChart-*.js` (recharts) | 355.6 KB | 92.6 KB |
| `WorkflowStudioPage-*.js` (ReactFlow) | 284.4 KB | 80.9 KB |
| `transform-*.js` (api transforms) | 131.3 KB | 39.5 KB |
| `proxy-*.js` (api client mutator) | 117.8 KB | 37.8 KB |
| `types-*.js` (api types index) | 91.2 KB | 24.5 KB |

**Soft thresholds** (set in `scripts/parse-bundle-stats.mjs`)

| Bucket | Threshold |
| --- | ---: |
| Initial raw | 400 KB |
| Initial gzip | 120 KB |
| Total raw | 3100 KB |
| Total gzip | 950 KB |

Chosen ~25-30% above baseline so PRs have headroom for normal feature
growth while still catching real regressions (a single page-component
mistakenly importing a 200 KB dep would trip).

**When to tighten**: revisit after ~20 PRs of trend data, OR when a
future bundle audit shows the buffer is excessive. Tightening a
threshold is a one-line edit in the parser.
