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
} as const;
