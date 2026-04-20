// Coverage for useMcpCatalog composition + live prompts fetch.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mockFetch, renderWithQueryClient } from "./__tests__/test-utils";

const { mocks } = vi.hoisted(() => ({
  mocks: {
    status: { data: { healthy: true }, isLoading: false, error: null },
    tools: { data: { items: [{ name: "cordum_list_jobs" }] }, isLoading: false, error: null },
    resources: { data: { items: ["cordum://jobs/{id}"] }, isLoading: false, error: null },
  },
}));

vi.mock("./useSettings", () => ({
  useMcpStatus: () => mocks.status,
  useMcpTools: () => mocks.tools,
  useMcpResources: () => mocks.resources,
}));

import { useMcpCatalog } from "./useMcpCatalog";

describe("useMcpCatalog", () => {
  let fetchSpy: ReturnType<typeof mockFetch>;

  beforeEach(() => {
    // Default — each test installs its own expected responses below.
  });

  afterEach(() => {
    fetchSpy?.mockRestore();
  });

  it("fetches prompts live from /mcp/prompts and merges UI metadata", async () => {
    fetchSpy = mockFetch([
      {
        match: "/api/v1/mcp/prompts",
        body: {
          prompts: [
            {
              name: "draft_safety_rule",
              description: "Draft YAML scaffold.",
              arguments: [
                { name: "scenario", description: "Goal", required: true },
                { name: "topic", description: "Topic pattern", required: false },
                { name: "risk_level", description: "low|medium|high", required: false },
              ],
            },
            {
              name: "explain_denial",
              description: "Explain denial.",
              arguments: [{ name: "job_id", description: "Job id", required: true }],
            },
            {
              name: "summarize_approvals",
              description: "Summarise approvals.",
              arguments: [
                { name: "window", description: "Time window", required: false },
                { name: "tenant", description: "Tenant", required: false },
              ],
            },
            {
              name: "policy_migration_helper",
              description: "Migrate grammar.",
              arguments: [
                { name: "from_version", description: "Source", required: true },
                { name: "to_version", description: "Target", required: true },
              ],
            },
          ],
        },
      },
    ]);

    const { result, waitFor, unmount } = renderWithQueryClient(() => useMcpCatalog());

    expect(result.current?.status).toBe(mocks.status);
    expect(result.current?.tools).toBe(mocks.tools);
    expect(result.current?.resources).toBe(mocks.resources);

    await waitFor(() => {
      expect(result.current?.prompts.isLoading).toBe(false);
      expect(result.current?.prompts.data?.length).toBe(4);
    });
    expect(result.current?.prompts.error).toBeNull();
    const data = result.current!.prompts.data!;
    const byName = Object.fromEntries(data.map((p) => [p.name, p]));
    expect(byName.draft_safety_rule.safetyDisclaimer).toBe(true);
    expect(byName.draft_safety_rule.modelClass).toBe("small");
    expect(byName.draft_safety_rule.docsHref).toBe(
      "/docs/mcp/prompts#draft_safety_rule",
    );
    expect(byName.explain_denial.safetyDisclaimer).toBe(false);
    expect(byName.summarize_approvals.modelClass).toBe("reasoning");
    expect(byName.policy_migration_helper.safetyDisclaimer).toBe(true);
    unmount();
  });

  it("handles empty server catalogue without crashing", async () => {
    fetchSpy = mockFetch([
      { match: "/api/v1/mcp/prompts", body: { prompts: [] } },
    ]);
    const { result, waitFor, unmount } = renderWithQueryClient(() => useMcpCatalog());
    await waitFor(() => {
      expect(result.current?.prompts.isLoading).toBe(false);
      expect(result.current?.prompts.data).toEqual([]);
    });
    expect(result.current?.prompts.error).toBeNull();
    unmount();
  });

  it("surfaces unknown-prompt entries without UI metadata", async () => {
    fetchSpy = mockFetch([
      {
        match: "/api/v1/mcp/prompts",
        body: {
          prompts: [
            {
              name: "experimental_prompt",
              description: "Newly registered server-side.",
              arguments: [],
            },
          ],
        },
      },
    ]);
    const { result, waitFor, unmount } = renderWithQueryClient(() => useMcpCatalog());
    await waitFor(() => {
      expect(result.current?.prompts.isLoading).toBe(false);
      expect(result.current?.prompts.data?.length).toBe(1);
    });
    const item = result.current!.prompts.data![0];
    expect(item.name).toBe("experimental_prompt");
    expect(item.modelClass).toBeUndefined();
    expect(item.safetyDisclaimer).toBe(false);
    expect(item.docsHref).toBeUndefined();
    unmount();
  });
});
