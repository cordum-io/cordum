import React, { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { OutputRule } from "../../types/policy";
import { mockFetch } from "../../hooks/__tests__/test-utils";
import { OutputRuleDetail } from "./OutputRuleDetail";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

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

vi.mock("../../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../../lib/logger", () => ({
  logger: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

function createTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
}

interface RenderResult {
  container: HTMLDivElement;
  unmount: () => void;
  waitFor: (assertion: () => void, timeoutMs?: number) => Promise<void>;
}

function renderDetail(rule: OutputRule | null): RenderResult {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root: Root = createRoot(container);
  const queryClient = createTestQueryClient();

  act(() => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter>
          <OutputRuleDetail rule={rule} onClose={() => {}} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });

  async function waitFor(assertion: () => void, timeoutMs = 2500): Promise<void> {
    const start = Date.now();
    while (true) {
      try {
        assertion();
        return;
      } catch (error) {
        if (Date.now() - start >= timeoutMs) throw error;
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 10));
        });
      }
    }
  }

  return {
    container,
    unmount: () => {
      act(() => {
        root.unmount();
      });
      queryClient.clear();
      container.remove();
    },
    waitFor,
  };
}

describe("OutputRuleDetail", () => {
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

  it("renders rule detail drawer and recent findings", async () => {
    mockFetch([
      {
        match: (url) =>
          url.includes("/policy/audit?") &&
          url.includes("type=output") &&
          url.includes("rule_id=out-secret"),
        method: "GET",
        body: {
          items: [
            {
              id: "audit-1",
              created_at: new Date().toISOString(),
              job_id: "job-1",
              rule_id: "out-secret",
              decision: "quarantine",
              reason: "secret leak detected",
              findings: [
                {
                  type: "secret_leak",
                  severity: "critical",
                  detail: "aws_access_key_id found",
                },
              ],
            },
          ],
        },
      },
    ]);

    const rule: OutputRule = {
      id: "out-secret",
      description: "Detects leaked cloud keys",
      topics: ["job.*"],
      scanners: ["secret"],
      patterns: ["AKIA[0-9A-Z]{16}"],
      decision: "quarantine",
      severity: "critical",
      enabled: true,
      reason: "secret leakage",
      match: {
        topics: ["job.*"],
        scanners: ["secret"],
      },
    };

    const view = renderDetail(rule);
    await view.waitFor(() => {
      expect(view.container.textContent).toContain("out-secret");
      expect(view.container.textContent).toContain("Recent Findings");
      expect(view.container.textContent).toContain("job-1");
      expect(view.container.textContent).toContain("secret_leak");
      expect(view.container.textContent).toContain("Triggers 24h");
    });
    view.unmount();
  });
});
