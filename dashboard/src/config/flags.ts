const isProd = import.meta.env.PROD === true;
const isTest = import.meta.env.MODE === "test";

export const FEATURE_FLAGS = {
  // Governance Timeline ships on by default in every environment. The
  // prior prod-default-off flag was a brownout shim while the backend
  // `/api/v1/governance/decisions` endpoint was merging — that endpoint
  // is live now, so the Job / Run detail pages expose the Governance
  // tab unconditionally.
  governanceTimeline: true,
  // Fixture mocks remain dev-only so a developer without a running
  // gateway can still exercise the timeline locally. Never true in prod
  // or test runs.
  governanceTimelineMocks:
    !isProd &&
    !isTest &&
    import.meta.env.VITE_GOVERNANCE_TIMELINE_MOCKS !== "false",
  // Evals page ships dark until the three backend sibling tasks land
  // (task-f34c528f dataset store, task-08a86cc0 extraction pipeline,
  // task-42b98ec6 runner) AND ops flip this on. Opt-in via env var so
  // internal previews can flip it without a redeploy.
  evalsPage: !isProd || import.meta.env.VITE_EVALS_PAGE === "true",
  // Dev-only msw handlers so operators can demo Evals locally before
  // the backend routes are live. Never true in prod or test runs.
  evalsPageMocks:
    !isProd &&
    !isTest &&
    import.meta.env.VITE_EVALS_PAGE_MOCKS !== "false",
  // Approval analytics widget ships dark in prod until the backend
  // endpoint (gateway handler + Policy Decision Log) is fully in tree
  // and exercised. Opt-in via VITE_APPROVAL_ANALYTICS=true so internal
  // previews can flip it without a redeploy.
  approvalAnalytics: !isProd || import.meta.env.VITE_APPROVAL_ANALYTICS === "true",
} as const;
