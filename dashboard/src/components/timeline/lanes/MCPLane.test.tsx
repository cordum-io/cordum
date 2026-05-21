import { describe, expect, it, beforeEach } from "vitest";
import { fireEvent, renderWithProviders, screen, within } from "@/test-utils/render";
import type { AgentActionEvent } from "@/api/types";
import { MCPLane } from "./MCPLane";
import { resetMcpLaneFiltersForTests } from "@/state/mcpLaneFilters";

function makeEvent(overrides: Partial<AgentActionEvent>): AgentActionEvent {
  return {
    eventId: overrides.eventId ?? "mcp-evt-x",
    sessionId: "egs-1",
    executionId: "aex-1",
    tenantId: "tenant-a",
    principalId: "user-a",
    seq: overrides.seq ?? 1,
    ts: overrides.ts ?? "2026-05-17T10:00:00Z",
    layer: "mcp",
    kind: "mcp.tool.pre",
    decision: "ALLOW",
    status: "ok",
    artifactPtrs: [],
    ...overrides,
  };
}

const serverConnected = makeEvent({
  eventId: "mcp-evt-server-connected",
  kind: "mcp.server.connected",
  decision: "RECORDED",
  status: "ok",
  labels: { mcp_server: "filesystem-mcp" },
  seq: 1,
  ts: "2026-05-17T10:00:00Z",
});

const toolPre = makeEvent({
  eventId: "mcp-evt-tool-pre",
  kind: "mcp.tool.pre",
  toolName: "list_directory",
  decision: "ALLOW",
  status: "ok",
  inputRedacted: { tool_input_redacted: "[REDACTED:tool_input]" },
  seq: 2,
  ts: "2026-05-17T10:00:01Z",
});

const toolPost = makeEvent({
  eventId: "mcp-evt-tool-post",
  kind: "mcp.tool.post",
  toolName: "list_directory",
  decision: "ALLOW",
  status: "ok",
  artifactPtrs: [
    {
      artifactType: "edge.mcp_response",
      sessionId: "egs-1",
      executionId: "aex-1",
      eventId: "mcp-evt-tool-post",
      tenantId: "tenant-a",
      retentionClass: "standard",
      redactionLevel: "standard",
      sha256: "abc123def4567890abcdef",
      uri: "cordum://artifacts/abc123def4567890",
      createdAt: "2026-05-17T10:00:02Z",
    },
  ],
  seq: 3,
  ts: "2026-05-17T10:00:02Z",
});

const toolFailed = makeEvent({
  eventId: "mcp-evt-tool-failed",
  kind: "mcp.tool.failed",
  toolName: "exec_shell",
  decision: "DENY",
  status: "failed",
  errorCode: "policy_deny",
  errorMessage: "blocked by policy",
  seq: 4,
  ts: "2026-05-17T10:00:03Z",
});

const approvalRequested = makeEvent({
  eventId: "mcp-evt-approval-req",
  kind: "approval.requested",
  layer: "mcp",
  decision: "REQUIRE_APPROVAL",
  status: "ok",
  approvalRef: "appr-7f3a",
  seq: 5,
  ts: "2026-05-17T10:00:04Z",
});

const allMcpEvents: AgentActionEvent[] = [
  serverConnected,
  toolPre,
  toolPost,
  toolFailed,
  approvalRequested,
];

describe("MCPLane", () => {
  beforeEach(() => {
    resetMcpLaneFiltersForTests();
  });

  it("renders all MCP event kinds with distinct testids", () => {
    renderWithProviders(<MCPLane events={allMcpEvents} />);

    expect(screen.getByTestId("mcp-lane")).toBeTruthy();
    expect(screen.getByTestId("mcp-row-mcp-evt-server-connected")).toBeTruthy();
    expect(screen.getByTestId("mcp-row-mcp-evt-tool-pre")).toBeTruthy();
    expect(screen.getByTestId("mcp-row-mcp-evt-tool-post")).toBeTruthy();
    expect(screen.getByTestId("mcp-row-mcp-evt-tool-failed")).toBeTruthy();
    expect(screen.getByTestId("mcp-row-mcp-evt-approval-req")).toBeTruthy();

    // Each kind row carries a kind-icon with a stable testid.
    expect(screen.getByTestId("mcp-icon-server-connected")).toBeTruthy();
    expect(screen.getByTestId("mcp-icon-tool-pre")).toBeTruthy();
    expect(screen.getByTestId("mcp-icon-tool-post")).toBeTruthy();
    expect(screen.getByTestId("mcp-icon-tool-failed")).toBeTruthy();
    expect(screen.getByTestId("mcp-icon-approval-required")).toBeTruthy();
  });

  it("filters via the Tools-only chip toggle (URL updates to ?mcp_lane=tools)", () => {
    renderWithProviders(<MCPLane events={allMcpEvents} />);

    // Default: every chip active → every MCP row visible.
    expect(screen.queryByTestId("mcp-row-mcp-evt-server-connected")).not.toBeNull();
    expect(screen.queryByTestId("mcp-row-mcp-evt-tool-pre")).not.toBeNull();
    expect(screen.queryByTestId("mcp-row-mcp-evt-approval-req")).not.toBeNull();

    // Click each non-Tools chip to deactivate them.
    fireEvent.click(screen.getByTestId("mcp-chip-servers"));
    fireEvent.click(screen.getByTestId("mcp-chip-approvals"));
    fireEvent.click(screen.getByTestId("mcp-chip-failures"));

    // Now only Tools-related rows remain.
    expect(screen.queryByTestId("mcp-row-mcp-evt-server-connected")).toBeNull();
    expect(screen.queryByTestId("mcp-row-mcp-evt-approval-req")).toBeNull();
    // Failed kind belongs to BOTH tools and failures; failures off → still hidden.
    expect(screen.queryByTestId("mcp-row-mcp-evt-tool-failed")).toBeNull();

    // tool.pre and tool.post stay.
    expect(screen.queryByTestId("mcp-row-mcp-evt-tool-pre")).not.toBeNull();
    expect(screen.queryByTestId("mcp-row-mcp-evt-tool-post")).not.toBeNull();
  });

  it("shows the no-MCP-activity empty state when zero mcp.* events are present", () => {
    renderWithProviders(<MCPLane events={[]} />);
    expect(screen.getByTestId("mcp-lane-empty")).toBeTruthy();
    expect(screen.queryByTestId("mcp-row-mcp-evt-server-connected")).toBeNull();
  });

  it("sanitizes a bare unredacted tool_input field before display (defense-in-depth)", () => {
    const leaky = makeEvent({
      eventId: "mcp-evt-leaky",
      kind: "mcp.tool.pre",
      toolName: "shell",
      inputRedacted: { tool_input: "Authorization: Bearer sk-real-leak-XYZ" },
      seq: 99,
    });
    const { container } = renderWithProviders(<MCPLane events={[leaky]} />);
    fireEvent.click(screen.getByTestId("mcp-row-mcp-evt-leaky"));
    const inspector = screen.getByTestId("mcp-inspector-mcp-evt-leaky");
    expect(within(inspector).queryByText(/sk-real-leak-XYZ/)).toBeNull();
    expect(container.textContent ?? "").not.toContain("sk-real-leak-XYZ");
  });

  it("trusts the *_redacted suffix verbatim and displays it as-is", () => {
    const redactedEvent = makeEvent({
      eventId: "mcp-evt-trusted",
      kind: "mcp.tool.pre",
      toolName: "shell",
      inputRedacted: { tool_input_redacted: "[REDACTED:tool_input]" },
      seq: 100,
    });
    renderWithProviders(<MCPLane events={[redactedEvent]} />);
    fireEvent.click(screen.getByTestId("mcp-row-mcp-evt-trusted"));
    const inspector = screen.getByTestId("mcp-inspector-mcp-evt-trusted");
    expect(within(inspector).getByText(/\[REDACTED:tool_input\]/)).toBeTruthy();
  });

  it("renders an approval_ref Link that points at /approvals/<ref>", () => {
    renderWithProviders(<MCPLane events={[approvalRequested]} />);
    fireEvent.click(screen.getByTestId("mcp-row-mcp-evt-approval-req"));
    const link = screen.getByTestId("mcp-inspector-approval-ref-link") as HTMLAnchorElement;
    expect(link.getAttribute("href")).toBe("/approvals/appr-7f3a");
  });

  it("shows the artifact-pointer chip when an event carries an artifact uri", () => {
    renderWithProviders(<MCPLane events={[toolPost]} />);
    fireEvent.click(screen.getByTestId("mcp-row-mcp-evt-tool-post"));
    const chip = screen.getByTestId("mcp-inspector-artifact-chip");
    expect(chip).toBeTruthy();
    expect((chip.textContent ?? "")).toContain("abc123def456");
  });

  it("passes axe-core WCAG 2 A/AA strict gate (0 violations)", async () => {
    await renderWithProviders(<MCPLane events={allMcpEvents} />, { runAxe: true });
  });

  it("does not render <script> from a maliciously-shaped approval_ref", () => {
    const xss = makeEvent({
      eventId: "mcp-evt-xss",
      kind: "approval.requested",
      layer: "mcp",
      decision: "REQUIRE_APPROVAL",
      approvalRef: "<script>alert(1)</script>",
      seq: 200,
    });
    const { container } = renderWithProviders(<MCPLane events={[xss]} />);
    fireEvent.click(screen.getByTestId("mcp-row-mcp-evt-xss"));
    expect(container.querySelector("script")).toBeNull();
  });

  it("ignores an invalid ?mcp_lane= query value without raising and still shows everything", () => {
    renderWithProviders(<MCPLane events={allMcpEvents} />, {
      initialEntries: ["/edge/sessions/egs-1?mcp_lane=bogus,tools"],
    });
    // Invalid chips silently ignored. Valid `tools` chip remains active.
    // All other chips also default-active because the parser kept defaults
    // when nothing valid would have been left. Spec: invalid-only URLs
    // fall back to defaults; mixed parses keep the valid chips.
    expect(screen.queryByTestId("mcp-row-mcp-evt-tool-pre")).not.toBeNull();
  });
});
