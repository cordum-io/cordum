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

```text
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

## ESLint plan for Pass C

Add a `no-console` rule scoped to `dashboard/src/**/*.{ts,tsx}` excluding
`src/test-utils/` and `*.test.*`. The single existing call site in
`src/api/transform.ts` will be migrated to `logger.warn` from
`@/lib/logger` (already exists).

## DoD reminder (per task-1acf9c07)

- Pass A: knip report committed; all dead code findings removed in batched commits. ✅ baseline shipped here.
- Pass B: at least 3 duplicated patterns factored to shared.
- Pass C: zero `console.*` in production `src/` paths; logger consistent; ESLint rule prevents regression.
- All 3 passes documented in this file with before/after metrics.
- tsc + vitest + build green; bundle size unchanged or smaller.
