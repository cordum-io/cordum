const isProd = import.meta.env.PROD === true;
const isTest = import.meta.env.MODE === "test";

export const FEATURE_FLAGS = {
  // Governance Timeline depends on the backend
  // `/api/v1/governance/decisions` endpoint landing in split/platform.
  // Keep it on in dev so local previews exercise the timeline, but
  // prod stays dark until ops explicitly flip it on via the env var —
  // this prevents a split-order race where the dashboard PR merges
  // before the backend endpoint is live.
  governanceTimeline:
    !isProd || import.meta.env.VITE_GOVERNANCE_TIMELINE === "true",
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
  // Delegation dashboard remains dark until the issuance, kernel, and
  // job-submit delegation flows are all in tree and the Phase 1-2 moat
  // is proven. Opt-in explicitly for previews.
  delegationDashboard:
    import.meta.env.VITE_DELEGATION_DASHBOARD === "true",
} as const;
