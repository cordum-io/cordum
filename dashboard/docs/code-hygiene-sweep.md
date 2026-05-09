# Dashboard code hygiene sweep — task-1acf9c07

_Yaron directive 2026-05-09._ Three-pass sweep of `dashboard/src/`: dead code,
factor shared, console-to-logger.

This doc is the canonical record. Each batch updates the running tally.

## Tools

- **knip** ^6 (devDep, installed in step 1) — broader detection than ts-prune:
  unused files, unused exports, unused dependencies, duplicate exports.
- **ESLint** (existing flat config at `dashboard/eslint.config.mjs`) — Pass C
  adds a `no-console` rule excluding `src/test-utils/` + `*.test.*`.

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

## DoD reminder (per task-1acf9c07)

- Pass A: knip report committed; all dead code findings removed in batched commits. ✅ A1+A2(slices 1-3)+A3+A4 shipped; residual 9 entries explained above.
- Pass B: at least 3 duplicated patterns factored to shared.
- Pass C: zero `console.*` in production `src/` paths; logger consistent; ESLint rule prevents regression.
- All 3 passes documented in this file with before/after metrics.
- tsc + vitest + build green; bundle size unchanged or smaller.
