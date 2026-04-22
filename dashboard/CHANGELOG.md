# Changelog

## Unreleased

- Added the Evals dashboard page (`/evals` + `/evals/:datasetId` + `/evals/runs/:runId`) turning denied incidents into a policy regression suite. Dataset list with score badges and regression dots, incident extraction dialog with dry-run preview and 409 collision messaging that respects dataset immutability, dataset detail view with Recharts score-trend chart (30-run window, red markers on regression runs) and run history, and a run detail view with filterable accordion drill-down of per-entry pass/fail/regression/error results. Shipped dark behind `FEATURE_FLAGS.evalsPage` (opt-in via `VITE_EVALS_PAGE=true`) until the three sibling backend tasks land, with dev-only fixture handlers under `src/mocks/handlers/evals.ts`.
- Added the Governance Timeline dashboard surface for job and workflow detail views. Enabled by default in every environment — the `/api/v1/governance/decisions` backend is live so the Governance tab is visible on `JobDetailPage.tsx` and `RunDetailPage.tsx` without any feature flag.
- `FEATURE_FLAGS.governanceTimeline` is retained as a permanently-true value only so existing imports compile; the prod-default-off gate that QA flagged has been removed.
- Development-only governance fixture handlers remain under `src/mocks/handlers/governance.ts` so a developer without a running gateway can still exercise the timeline locally. Mocks never load in production or test builds.
