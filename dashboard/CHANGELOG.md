# Changelog

## Unreleased

- Added the Governance Timeline dashboard surface for job and workflow detail views. Enabled by default in every environment — the `/api/v1/governance/decisions` backend is live so the Governance tab is visible on `JobDetailPage.tsx` and `RunDetailPage.tsx` without any feature flag.
- `FEATURE_FLAGS.governanceTimeline` is retained as a permanently-true value only so existing imports compile; the prod-default-off gate that QA flagged has been removed.
- Development-only governance fixture handlers remain under `src/mocks/handlers/governance.ts` so a developer without a running gateway can still exercise the timeline locally. Mocks never load in production or test builds.
