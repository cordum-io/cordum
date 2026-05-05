import { describe, expect, it, vi } from "vitest";
import type { AgentActionEvent } from "@/api/types";
import { fireEvent, renderWithProviders, screen, within } from "@/test-utils/render";
import { EdgeEventInspector } from "./EdgeEventInspector";

function makeEvent(overrides: Partial<AgentActionEvent> = {}): AgentActionEvent {
  return {
    eventId: "edge_evt_42",
    sessionId: "edge_sess_1",
    executionId: "edge_exec_1",
    tenantId: "tenant-a",
    principalId: "user-a",
    seq: 7,
    ts: "2026-05-02T16:00:01Z",
    layer: "pre_tool_use",
    kind: "hook.pre_tool_use",
    toolName: "Read",
    actionName: "Read fixture/.env",
    capability: "filesystem.read",
    riskTags: ["secret_access", "filesystem"],
    inputRedacted: { path_class: "secret", redacted_marker: true },
    inputHash: "hash-input-1",
    decision: "DENY",
    decisionReason: "Sensitive file (deny-secret-reads)",
    ruleId: "claude-code.deny-secret-reads",
    policySnapshot: "policy-v3",
    approvalRef: undefined,
    artifactPtrs: [
      {
        artifactType: "edge.tool_result",
        sessionId: "edge_sess_1",
        executionId: "edge_exec_1",
        eventId: "edge_evt_42",
        tenantId: "tenant-a",
        retentionClass: "standard",
        redactionLevel: "standard",
        sha256:
          "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        uri: "artifact://edge/edge_sess_1/edge_exec_1/edge_evt_42/result",
        createdAt: "2026-05-02T16:00:01Z",
        sizeBytes: 2048,
        contentType: "application/json",
      },
    ],
    durationMs: 12,
    status: "recorded",
    errorCode: undefined,
    errorMessage: undefined,
    ...overrides,
  };
}

describe("EdgeEventInspector", () => {
  it("renders event identifiers, decision, hash, and artifact metadata", () => {
    renderWithProviders(<EdgeEventInspector event={makeEvent()} open onClose={vi.fn()} />);
    const inspector = screen.getByTestId("edge-event-inspector");
    const within$ = within(inspector);
    expect(within$.getByTestId("edge-event-id").textContent).toContain("edge_evt_42");
    expect(within$.getByTestId("edge-event-decision").textContent).toContain("DENY");
    expect(within$.getByText("hash-input-1")).toBeTruthy();
    expect(within$.getByText("Read")).toBeTruthy();
    expect(within$.getByText("hook.pre_tool_use")).toBeTruthy();
    expect(within$.getByText("claude-code.deny-secret-reads")).toBeTruthy();
    expect(within$.getByText("Sensitive file (deny-secret-reads)")).toBeTruthy();
    const artifacts = within$.getByTestId("edge-event-artifacts");
    expect(artifacts.textContent).toContain("2.0 KiB");
    expect(artifacts.textContent).toContain("edge.tool_result");
    expect(artifacts.textContent).toContain("artifact://edge/edge_sess_1/edge_exec_1/edge_evt_42/result");
  });

  it("renders an empty-state message when there are no artifact pointers", () => {
    renderWithProviders(
      <EdgeEventInspector event={makeEvent({ artifactPtrs: [] })} open onClose={vi.fn()} />,
    );
    expect(screen.queryByTestId("edge-event-artifacts")).toBeNull();
    expect(screen.getByText(/No artifact pointers attached/i)).toBeTruthy();
  });

  it("serializes the redacted summary into a code block", () => {
    renderWithProviders(<EdgeEventInspector event={makeEvent()} open onClose={vi.fn()} />);
    const inspector = screen.getByTestId("edge-event-inspector");
    expect(inspector.textContent).toContain("path_class");
    expect(inspector.textContent).toContain("redacted_marker");
  });

  it("renders an em-dash placeholder when no approval ref is attached", () => {
    renderWithProviders(<EdgeEventInspector event={makeEvent({ approvalRef: undefined })} open onClose={vi.fn()} />);
    const approvalRef = screen.getByTestId("edge-event-approval-ref");
    expect(approvalRef.textContent).toBe("—");
  });

  it("calls onClose when the backdrop button fires", () => {
    const onClose = vi.fn();
    renderWithProviders(<EdgeEventInspector event={makeEvent()} open onClose={onClose} />);
    fireEvent.click(screen.getByLabelText("Close"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("renders fallback when no event is selected", () => {
    renderWithProviders(<EdgeEventInspector event={null} open onClose={vi.fn()} />);
    expect(screen.getByText(/No event selected/i)).toBeTruthy();
  });

  it("renders nothing when closed", () => {
    const { container } = renderWithProviders(
      <EdgeEventInspector event={makeEvent()} open={false} onClose={vi.fn()} />,
    );
    expect(container.querySelector('[data-testid="edge-event-inspector"]')).toBeNull();
  });
});
