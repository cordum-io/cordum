import { describe, expect, it } from "vitest";
import { renderWithProviders, screen, within } from "@/test-utils/render";
import type { AgentActionEvent } from "@/api/types";
import { MCPInspector } from "./MCPInspector";

function makeEvent(overrides: Partial<AgentActionEvent>): AgentActionEvent {
  return {
    eventId: overrides.eventId ?? "mcp-evt-inspector",
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

describe("MCPInspector", () => {
  it("renders all 6 fields (upstream, tool, decision, approval_ref, args, result)", () => {
    const event = makeEvent({
      eventId: "mcp-evt-full",
      kind: "mcp.tool.post",
      toolName: "list_directory",
      decision: "ALLOW",
      approvalRef: "appr-abc",
      labels: {
        mcp_server: "filesystem-mcp",
        result_redacted: "[REDACTED:result]",
      },
      inputRedacted: { tool_input_redacted: "[REDACTED:tool_input]" },
    });

    renderWithProviders(<MCPInspector event={event} />);
    const inspector = screen.getByTestId("mcp-inspector-mcp-evt-full");

    expect(within(inspector).getByTestId("mcp-inspector-upstream-server").textContent).toContain(
      "filesystem-mcp",
    );
    expect(within(inspector).getByTestId("mcp-inspector-tool-name").textContent).toContain(
      "list_directory",
    );
    expect(within(inspector).getByTestId("mcp-inspector-decision").textContent).toContain("ALLOW");
    expect(within(inspector).getByTestId("mcp-inspector-approval-ref-link")).toBeTruthy();
    expect(within(inspector).getByTestId("mcp-inspector-args").textContent).toContain(
      "[REDACTED:tool_input]",
    );
    expect(within(inspector).getByTestId("mcp-inspector-result").textContent).toContain(
      "[REDACTED:result]",
    );
  });

  it("shows an em-dash for upstream when neither label nor agentProduct is present", () => {
    const event = makeEvent({
      eventId: "mcp-evt-nolabel",
      labels: undefined,
      agentProduct: undefined,
    });
    renderWithProviders(<MCPInspector event={event} />);
    expect(screen.getByTestId("mcp-inspector-upstream-server").textContent).toContain("—");
  });

  it("renders a static span (not a Link) for approval_ref when unset", () => {
    const event = makeEvent({
      eventId: "mcp-evt-no-approval",
      approvalRef: undefined,
    });
    renderWithProviders(<MCPInspector event={event} />);
    expect(screen.queryByTestId("mcp-inspector-approval-ref-link")).toBeNull();
    expect(screen.getByTestId("mcp-inspector-approval-ref").textContent).toContain("—");
  });
});
