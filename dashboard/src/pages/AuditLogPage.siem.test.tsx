import { describe, expect, it } from "vitest";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import {
  http,
  HttpResponse,
  server,
} from "@/test-utils/msw";
import {
  renderWithProviders,
  waitFor,
} from "@/test-utils/render";
import AuditLogPage from "./AuditLogPage";

// AuditLogPage_SIEM — render-level coverage for the audit-log page after
// it switches from /policy/audit (policy-bundle subset) to /audit/events
// (full SIEM feed). Pre-merge this test is RED because the page is still
// wired to /policy/audit; the step-8 refactor moves it to the new feed
// and turns this green without touching the assertions.
describe("AuditLogPage_SIEM render coverage", () => {
  it("renders MCP, Edge, and Worker event types from the /audit/events feed", async () => {
    server.use(
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items: [
            {
              id: "siem-mcp",
              seq: 100,
              timestamp: "2026-05-15T12:00:00Z",
              event_type: "mcp.tool_invocation",
              severity: "INFO",
              tenant_id: "default",
              action: "invoke",
              identity: "alice",
              extra: { tool_name: "search.web" },
            },
            {
              id: "siem-edge",
              seq: 99,
              timestamp: "2026-05-15T11:59:59Z",
              event_type: "edge.action_attempted",
              severity: "INFO",
              tenant_id: "default",
              action: "attempt",
              identity: "bob",
            },
            {
              id: "siem-worker",
              seq: 98,
              timestamp: "2026-05-15T11:59:58Z",
              event_type: "worker_trust_change",
              severity: "INFO",
              tenant_id: "default",
              action: "trust",
              identity: "worker-7",
            },
            {
              id: "policy-evt",
              seq: 97,
              timestamp: "2026-05-15T11:59:57Z",
              event_type: "policy.bundle.update",
              severity: "INFO",
              tenant_id: "default",
              action: "update",
              identity: "carol",
            },
          ],
          next_cursor: "",
          returned: 4,
        }),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/audit"] },
    );

    // The table shows the SIEM `action` verb in the Action column and
    // the `identity` in the Actor column; the resource family (mcp /
    // edge / worker / policy) lands in the Resource column via
    // siemResourceTypeFromEventType. Asserting on identity strings is
    // the cleanest invariant — they're unique per row and prove the
    // SIEM feed has been mapped into the table rather than the legacy
    // /policy/audit shape leaking through.
    await waitFor(() => {
      const text = container.textContent ?? "";
      expect(text).toContain("alice");
    });

    const rendered = container.textContent ?? "";
    expect(rendered).toContain("alice"); // mcp.tool_invocation row
    expect(rendered).toContain("bob"); // edge.action_attempted row
    expect(rendered).toContain("worker-7"); // worker_trust_change row
    expect(rendered).toContain("carol"); // policy.bundle.update row
    // Resource families derived from event_type prefixes.
    expect(rendered).toContain("mcp");
    expect(rendered).toContain("edge");
    expect(rendered).toContain("worker");
    expect(rendered).toContain("policy");
  });
});
