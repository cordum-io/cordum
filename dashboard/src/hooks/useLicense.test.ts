import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import { __licenseInternal, useLicense, useLicenseOverview, useLicenseUsage } from "./useLicense";

const { loggerMock } = vi.hoisted(() => ({
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

const { mockConfigState } = vi.hoisted(() => ({
  mockConfigState: {
    apiBaseUrl: "/api/v1",
    apiKey: "",
    tenantId: "",
    principalId: "",
    principalRole: "",
    user: null,
    logout: vi.fn(),
  },
}));

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../lib/logger", () => ({
  logger: loggerMock,
}));

describe("useLicense hooks", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockConfigState.apiBaseUrl = "/api/v1";
    mockConfigState.apiKey = "";
    mockConfigState.tenantId = "";
    mockConfigState.principalId = "";
    mockConfigState.principalRole = "";
    mockConfigState.user = null;
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000123");
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("maps /license into camelCase frontend types", async () => {
    mockFetch([
      {
        match: "/license",
        method: "GET",
        body: {
          plan: "team",
          expiry_status: "warning",
          entitlements: {
            approval_mode: "multi",
            max_workers: 25,
            max_concurrent_jobs: 25,
            audit_retention_days: 90,
            siem_export: false,
            agent_identity: true,
          },
          rights: {
            hosted_service: true,
            embedding: false,
            resale: false,
            white_label: true,
            support_sla: false,
          },
          license: {
            mode: "file",
            status: "warning",
            plan: "team",
            org_id: "org-1",
            license_id: "lic-123",
            expires_at: "2026-05-01T00:00:00Z",
          },
        },
      },
    ]);

    const queryClient = createTestQueryClient();
    const hook = renderWithQueryClient(() => useLicense(), queryClient);

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data).toMatchObject({
      plan: "team",
      expiryStatus: "warning",
      entitlements: {
        approvalMode: "multi",
        maxWorkers: 25,
        maxConcurrentJobs: 25,
        auditRetentionDays: 90,
        siemExport: false,
        agentIdentity: true,
      },
      rights: {
        hostedService: true,
        whiteLabel: true,
      },
      license: {
        orgId: "org-1",
        licenseId: "lic-123",
        expiresAt: "2026-05-01T00:00:00Z",
      },
    });

    const query = queryClient.getQueryCache().find({ queryKey: ["license"] });
    const options = query?.options as { refetchInterval?: number } | undefined;
    expect(options?.refetchInterval).toBe(60_000);

    hook.unmount();
  });

  it("maps /license/usage metrics into camelCase usage keys", async () => {
    mockFetch([
      {
        match: "/license/usage",
        method: "GET",
        body: {
          tenant_id: "tenant-a",
          plan: "community",
          usage: {
            workers: { current: 2, allowed: 3, registered: 2, connected: 2 },
            concurrent_jobs: { current: 1, allowed: 3 },
            active_workflows: { current: 0, allowed: 2 },
            workflow_steps: { allowed: 10 },
            schemas: { current: 4, allowed: 25 },
            policy_bundles: { current: 0, allowed: 0 },
            requests_per_second: { allowed: 500 },
            prompt_chars: { allowed: 16000 },
            body_bytes: { allowed: 1048576 },
            approval_mode: { allowed: "single" },
          },
        },
      },
    ]);

    const queryClient = createTestQueryClient();
    const hook = renderWithQueryClient(() => useLicenseUsage(), queryClient);

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data).toMatchObject({
      tenantId: "tenant-a",
      plan: "community",
      usage: {
        workers: { current: 2, allowed: 3, registered: 2, connected: 2 },
        concurrentJobs: { current: 1, allowed: 3 },
        activeWorkflows: { current: 0, allowed: 2 },
        workflowSteps: { allowed: 10 },
        schemas: { current: 4, allowed: 25 },
        policyBundles: { current: 0, allowed: 0 },
        requestsPerSecond: { allowed: 500 },
        promptChars: { allowed: 16000 },
        bodyBytes: { allowed: 1048576 },
        approvalMode: { allowed: "single" },
      },
    });

    const query = queryClient.getQueryCache().find({ queryKey: ["license", "usage"] });
    const options = query?.options as { refetchInterval?: number } | undefined;
    expect(options?.refetchInterval).toBe(15_000);

    hook.unmount();
  });

  it("useLicenseOverview combines the two queries", async () => {
    mockFetch([
      {
        match: "/license/usage",
        method: "GET",
        body: {
          tenant_id: "tenant-a",
          plan: "community",
          usage: {
            workers: { current: 1, allowed: 3 },
            concurrent_jobs: { current: 0, allowed: 3 },
            active_workflows: { current: 0, allowed: 2 },
            workflow_steps: { allowed: 10 },
            schemas: { current: 0, allowed: 25 },
            policy_bundles: { current: 0, allowed: 0 },
            requests_per_second: { allowed: 500 },
            prompt_chars: { allowed: 16000 },
            body_bytes: { allowed: 1048576 },
            approval_mode: { allowed: "single" },
          },
        },
      },
      {
        match: "/license",
        method: "GET",
        body: {
          plan: "community",
          entitlements: {
            approval_mode: "single",
            max_workers: 3,
          },
          rights: null,
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useLicenseOverview());

    await hook.waitFor(() => {
      expect(hook.result.current?.isLoading).toBe(false);
    });

    expect(hook.result.current?.isError).toBe(false);
    expect(hook.result.current?.license.data?.plan).toBe("community");
    expect(hook.result.current?.usage.data?.tenantId).toBe("tenant-a");

    hook.unmount();
  });

  it("internal mappers preserve null/empty states", () => {
    expect(__licenseInternal.mapLicenseRights(undefined)).toBeNull();
    expect(__licenseInternal.mapLicenseInfo(null)).toBeNull();
    expect(__licenseInternal.mapLicenseSummary({})).toMatchObject({
      plan: "community",
      entitlements: {},
      rights: null,
    });
    expect(__licenseInternal.mapLicenseUsageSummary({})).toMatchObject({
      tenantId: "",
      plan: "community",
      usage: {
        workers: {},
        approvalMode: {},
      },
    });
  });

  it("maps agent_identity feature flags into the dashboard entitlement model", () => {
    expect(
      __licenseInternal.mapLicenseEntitlements({
        agent_identity: true,
      }),
    ).toMatchObject({
      agentIdentity: true,
    });
  });
});
