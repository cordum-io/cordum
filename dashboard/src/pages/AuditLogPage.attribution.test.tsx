import { describe, expect, it } from "vitest";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { http, HttpResponse, server } from "@/test-utils/msw";
import {
  renderWithProviders,
  fireEvent,
  screen,
  waitFor,
} from "@/test-utils/render";
import AuditLogPage from "./AuditLogPage";
import { assertNoSeriousAxeViolations } from "@/test-utils/a11y";

// Render-level coverage for the human-readable audit attribution UI
// (task-c8d4b056): summary text in the table + drawer, the Hide system/routine
// toggle (category=governance), agent visibility, and the empty-state copy.

const SIEM_ROW = {
  id: "siem-edge-denied",
  seq: 100,
  timestamp: "2026-05-15T12:00:00Z",
  event_type: "edge.action_denied",
  severity: "HIGH",
  tenant_id: "default",
  agent_id: "agent-7",
  agent_name: "Billing Bot",
  action: "edge.action",
  decision: "deny",
  matched_rule: "no-prod-writes",
  reason: "blocked path",
  identity: "user:alice",
  human_summary: "Billing Bot was denied Bash — deny (blocked path)",
  actor_label: "Alice Ops",
  agent_label: "Billing Bot",
  resource_label: "rm -rf /etc",
  category: "governance",
  agent_product: "claude-code",
  session_id: "sess-9",
  execution_id: "exec-3",
  trace_id: "tr-1",
  artifact_id: "sha256:abc",
  input_preview: "command: read config",
  extra: { tool_name: "Bash", session_id: "sess-9" },
};

function auditFeed(items: unknown[], record?: (url: URL) => void) {
  return http.get("*/api/v1/audit/events", ({ request }) => {
    record?.(new URL(request.url));
    return HttpResponse.json({ items, next_cursor: "", returned: items.length });
  });
}

describe("AuditLogPage human-readable attribution", () => {
  it("renders the human summary + agent label in the table row", async () => {
    server.use(auditFeed([SIEM_ROW]));
    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/audit"] },
    );
    await waitFor(() => {
      expect(container.textContent ?? "").toContain("Billing Bot was denied Bash");
    });
    // A11y regression gate for this customer-visible surface. Per the repo
    // convention (dashboard/CLAUDE.md), async-data page tests assert axe AFTER
    // the loaded paint rather than via the synchronous runAxe sugar.
    await assertNoSeriousAxeViolations(container);
    const text = container.textContent ?? "";
    expect(text).toContain("Billing Bot"); // agent label / pivot
    expect(text).toContain("deny"); // decision badge
  });

  it("Hide system/routine toggle narrows the feed to category=governance", async () => {
    const urls: URL[] = [];
    server.use(auditFeed([SIEM_ROW], (u) => urls.push(u)));
    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/audit"] },
    );
    await waitFor(() => expect(urls.length).toBeGreaterThan(0));
    // Initially no category filter.
    expect(urls[0].searchParams.get("category")).toBeNull();

    const toggle = await screen.findByRole("button", {
      name: /hide system\/routine audit/i,
    });
    fireEvent.click(toggle);

    // Canonical proof: toggling narrows the server feed to category=governance.
    await waitFor(() => {
      expect(
        urls.some((u) => u.searchParams.get("category") === "governance"),
      ).toBe(true);
    });
  });

  it("shows a system/routine-hidden empty state with a clear-filter action", async () => {
    server.use(auditFeed([]));
    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="hide_system=1">
        <AuditLogPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/audit?hide_system=1"] },
    );
    await waitFor(() => {
      expect(container.textContent ?? "").toContain("system/routine rows are hidden");
    });
    // A clear-filters affordance is offered (header + empty-state).
    expect(
      screen.getAllByRole("button", { name: /clear filters/i }).length,
    ).toBeGreaterThan(0);
  });

  it("opens a drawer with attribution + a redacted input preview, no raw extra payload", async () => {
    server.use(auditFeed([SIEM_ROW]));
    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/audit"] },
    );
    const summaryCell = await screen.findByText(/Billing Bot was denied Bash/i);
    fireEvent.click(summaryCell);

    await waitFor(() => {
      // The drawer surfaces the attribution + redacted preview.
      expect(screen.getByText("Who / what")).toBeTruthy();
    });
    const drawerText = container.textContent ?? "";
    expect(drawerText).toContain("Alice Ops"); // actor label
    expect(drawerText).toContain("agent-7"); // agent id pivot
    expect(drawerText).toContain("command: read config"); // redacted input preview
    expect(drawerText).toContain("Evidence"); // preview section heading
    // Raw extra map (e.g. the duplicated tool_input style payload) is never
    // dumped into the drawer as a JSON blob.
    expect(drawerText).not.toContain('{"tool_name"');
  });

  it("warns visibly that only a partial slice is loaded when more rows exist", async () => {
    server.use(
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items: [SIEM_ROW],
          next_cursor: "9999-0", // more pages remain
          returned: 1,
        }),
      ),
    );
    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/audit"] },
    );
    await waitFor(() => {
      expect(container.textContent ?? "").toContain("more available");
    });
    // The export control is labelled as the visible slice, distinct from the
    // audit-complete compliance bundle.
    expect(
      screen.getByRole("button", { name: /export visible \(csv\)/i }),
    ).toBeTruthy();
  });
});
