import React, { act } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import LicensePage from "./LicensePage";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { apiState, hookState } = vi.hoisted(() => ({
  apiState: {
    get: vi.fn(),
  },
  hookState: {
    overview: {} as any,
  },
}));

vi.mock("@/api/client", () => ({
  get: apiState.get,
}));

vi.mock("@/hooks/useLicense", () => ({
  useLicenseOverview: () => hookState.overview,
  useReloadLicense: () => ({ mutate: vi.fn(), isPending: false }),
}));

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
}

function renderPage() {
  const queryClient = createTestQueryClient();
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={["/settings/license"]}>
          <LicensePage />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });

  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      queryClient.clear();
      container.remove();
    },
  };
}

async function waitFor(assertion: () => void, timeoutMs = 2000) {
  const start = Date.now();
  while (true) {
    try {
      assertion();
      return;
    } catch (error) {
      if (Date.now() - start >= timeoutMs) {
        throw error;
      }
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 10));
      });
    }
  }
}

describe("LicensePage", () => {
  beforeEach(() => {
    apiState.get.mockReset();
    hookState.overview = {
      license: {
        data: {
          plan: "enterprise",
          expiryStatus: "warning",
          entitlements: {
            approvalMode: "multi",
            telemetryMode: "anonymous",
            maxWorkers: 10,
            maxConcurrentJobs: 20,
            maxWorkflowSteps: 120,
            maxActiveWorkflows: 6,
            maxSchemaCount: 50,
            maxPolicyBundles: 10,
            requestsPerSecond: 250,
            maxPromptChars: 8000,
            maxBodyBytes: 1048576,
            maxArtifactBytes: 20971520,
            auditRetentionDays: 365,
            sso: true,
            saml: true,
            scim: true,
            rbac: true,
            audit: true,
            auditExport: true,
            siemExport: true,
            legalHold: true,
            velocityRules: true,
            breakGlassAdmin: true,
          },
          rights: {
            hostedService: true,
            embedding: true,
            resale: false,
            whiteLabel: true,
            supportSla: true,
          },
          license: {
            mode: "enterprise",
            orgId: "acme-co",
            licenseId: "lic-123",
            deploymentType: "hybrid",
            issuedAt: "2026-01-01T00:00:00Z",
            notBefore: "2026-01-01T00:00:00Z",
            expiresAt: "2026-05-01T00:00:00Z",
          },
        },
        error: null,
        refetch: vi.fn(),
      },
      usage: {
        data: {
          tenantId: "default",
          plan: "enterprise",
          usage: {
            workers: { current: 8, allowed: 10, registered: 8, connected: 7 },
            concurrentJobs: { current: 6, allowed: 20 },
            activeWorkflows: { current: 5, allowed: 6 },
            workflowSteps: { allowed: 120 },
            schemas: { current: 20, allowed: 50 },
            policyBundles: { current: 9, allowed: 10 },
            requestsPerSecond: { allowed: 250 },
            promptChars: { allowed: 8000 },
            bodyBytes: { allowed: 1048576 },
            approvalMode: { allowed: "multi" },
          },
        },
        error: null,
        refetch: vi.fn(),
      },
      isLoading: false,
      isError: false,
    };
    apiState.get.mockResolvedValue({
      mode: "local_only",
      endpoint: "https://telemetry.cordum.test",
      last_collected_at: "2026-04-08T00:00:00Z",
      last_reported_at: "2026-04-08T12:00:00Z",
    });
  });

  it("shows plan details, telemetry state, and upgrade pressure", async () => {
    const { container, cleanup } = renderPage();

    try {
      await waitFor(() => {
        expect(container.textContent).toContain("License & Limits");
        expect(container.textContent).toContain("Enterprise");
        expect(container.textContent).toContain("acme-co");
        expect(container.textContent).toContain("Runtime ceilings");
        expect(container.textContent).toContain("Workers nearing its tier limit");
        expect(container.textContent).toContain("Policy bundles nearing its tier limit");
        expect(container.textContent).toContain("Local only");
        expect(container.textContent).toContain("Single sign-on");
      });
    } finally {
      cleanup();
    }
  });

  it("shows the community fallback notice when no signed license is loaded", () => {
    hookState.overview = {
      license: {
        data: {
          plan: "community",
          entitlements: {},
          rights: null,
          license: null,
        },
        error: null,
        refetch: vi.fn(),
      },
      usage: {
        data: {
          tenantId: "default",
          plan: "community",
          usage: {
            workers: { current: 2, allowed: 3 },
            concurrentJobs: { current: 1, allowed: 5 },
            activeWorkflows: { current: 1, allowed: 2 },
            workflowSteps: { allowed: 20 },
            schemas: { current: 1, allowed: 5 },
            policyBundles: { current: 0, allowed: 2 },
            requestsPerSecond: { allowed: 50 },
            promptChars: { allowed: 4000 },
            bodyBytes: { allowed: 65536 },
            approvalMode: { allowed: "single" },
          },
        },
        error: null,
        refetch: vi.fn(),
      },
      isLoading: false,
      isError: false,
    };

    const { container, cleanup } = renderPage();

    try {
      expect(container.textContent).toContain("Community defaults are active");
      expect(container.textContent).toContain("No signed license is loaded for this deployment");
    } finally {
      cleanup();
    }
  });
});
