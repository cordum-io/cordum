import type { ComponentProps } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/api/client";
import type { AgentActionEvent, EdgeApproval } from "@/api/types";
import { fireEvent, renderWithProviders, screen, within } from "@/test-utils/render";
import {
  useApproveEdgeApproval,
  useEdgeApprovals,
  useRejectEdgeApproval,
} from "@/hooks/useEdgeSessions";
import { EdgeApprovalsDrawer } from "./EdgeApprovalsDrawer";

vi.mock("@/hooks/useEdgeSessions", () => ({
  useEdgeApprovals: vi.fn(),
  useApproveEdgeApproval: vi.fn(),
  useRejectEdgeApproval: vi.fn(),
}));

const refetch = vi.fn();
const approveMutate = vi.fn();
const rejectMutate = vi.fn();

function makeApproval(overrides: Partial<EdgeApproval> = {}): EdgeApproval {
  return {
    approvalRef: "edge_appr_1",
    tenantId: "tenant-a",
    sessionId: "edge_sess_1",
    executionId: "edge_exec_1",
    eventId: "edge_evt_1",
    principalId: "user-a",
    requester: "user-a",
    status: "pending",
    decision: "",
    reason: "Risky file read requires operator review",
    ruleId: "rule-edge-read",
    policySnapshot: "policy-v3",
    actionHash: "hash-action-1",
    inputHash: "hash-input-1",
    createdAt: "2026-05-02T16:00:00Z",
    expiresAt: "2026-05-02T18:00:00Z",
    resolvedAt: null,
    consumedAt: null,
    metadata: { action: "Read secrets.env" },
    ...overrides,
  };
}

const actionEvent: AgentActionEvent = {
  eventId: "edge_evt_1",
  sessionId: "edge_sess_1",
  executionId: "edge_exec_1",
  tenantId: "tenant-a",
  principalId: "user-a",
  seq: 7,
  ts: "2026-05-02T16:00:01Z",
  layer: "pre_tool_use",
  kind: "tool",
  toolName: "Read",
  actionName: "Read secrets.env",
  capability: "filesystem.read",
  riskTags: ["secret_access", "filesystem"],
  inputRedacted: {
    path: "src/secrets.env",
    raw_payload: "NEVER_RENDER_RAW",
    nested: { mode: "read", command_output: "NEVER_RENDER_OUTPUT" },
  },
  inputHash: "hash-input-1",
  decision: "require_approval",
  decisionReason: "Sensitive file",
  ruleId: "rule-edge-read",
  policySnapshot: "policy-v3",
  approvalRef: "edge_appr_1",
  artifactPtrs: [],
  status: "recorded",
};

function mockHooks(approvals: EdgeApproval[], error: ApiError | null = null) {
  vi.mocked(useEdgeApprovals).mockReturnValue({
    data: error ? undefined : { items: approvals, nextCursor: null },
    error,
    isLoading: false,
    isFetching: false,
    refetch,
  } as unknown as ReturnType<typeof useEdgeApprovals>);
  vi.mocked(useApproveEdgeApproval).mockReturnValue({
    mutate: approveMutate,
    isPending: false,
    error: null,
  } as unknown as ReturnType<typeof useApproveEdgeApproval>);
  vi.mocked(useRejectEdgeApproval).mockReturnValue({
    mutate: rejectMutate,
    isPending: false,
    error: null,
  } as unknown as ReturnType<typeof useRejectEdgeApproval>);
}

function renderDrawer(props: Partial<ComponentProps<typeof EdgeApprovalsDrawer>> = {}) {
  return renderWithProviders(
    <EdgeApprovalsDrawer
      open
      onClose={vi.fn()}
      sessionId="edge_sess_1"
      events={[actionEvent]}
      currentPrincipalId="reviewer-b"
      {...props}
    />,
  );
}

describe("EdgeApprovalsDrawer", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-05-02T16:30:00Z"));
    refetch.mockReset();
    approveMutate.mockReset();
    rejectMutate.mockReset();
    vi.mocked(useEdgeApprovals).mockReset();
    vi.mocked(useApproveEdgeApproval).mockReset();
    vi.mocked(useRejectEdgeApproval).mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders approval context and redacted event input without raw payload fields", () => {
    mockHooks([
      makeApproval(),
      makeApproval({ approvalRef: "edge_appr_2", status: "approved", decision: "approve" }),
    ]);

    renderDrawer();

    expect(screen.getAllByText("edge_appr_1").length).toBeGreaterThan(0);
    expect(screen.queryByText("Read secrets.env")).not.toBeNull();
    expect(screen.queryByText("secret_access")).not.toBeNull();
    expect(screen.queryByText("user-a")).not.toBeNull();
    expect(screen.queryByText("policy-v3")).not.toBeNull();
    expect(screen.queryByText("hash-action-1")).not.toBeNull();
    expect(screen.queryByText(/After approval, the agent must retry the same action hash/i)).not.toBeNull();
    expect(screen.queryByText(/src\/secrets.env/)).not.toBeNull();
    expect(screen.queryByText(/NEVER_RENDER_RAW|NEVER_RENDER_OUTPUT|raw_payload|command_output/)).toBeNull();
  });

  it("warns for self approval, stale approvals, and terminal records", () => {
    mockHooks([
      makeApproval({ expiresAt: "2026-05-02T10:00:00Z" }),
      makeApproval({ approvalRef: "edge_appr_2", status: "approved", decision: "approve" }),
    ]);

    renderDrawer({ currentPrincipalId: "user-a" });

    expect(screen.queryByText("Self-approval warning")).not.toBeNull();
    expect(screen.queryByText("Approval expired")).not.toBeNull();
    expect((screen.getByRole("button", { name: /^Approve$/i }) as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByText("edge_appr_2"));
    expect(screen.queryByText("Terminal approval")).not.toBeNull();
  });

  it("confirms approve and reject mutations with reviewer notes", () => {
    mockHooks([makeApproval()]);
    renderDrawer();

    fireEvent.click(screen.getByRole("button", { name: /^Approve$/i }));
    fireEvent.click(within(screen.getByRole("dialog", { name: /Approve Edge action/i })).getByRole("button", { name: "Approve" }));
    expect(approveMutate.mock.calls[0]?.[0]).toEqual({ approvalRef: "edge_appr_1", reason: "" });

    fireEvent.change(screen.getByPlaceholderText(/Optional reviewer note/i), {
      target: { value: "Snapshot no longer matches policy intent" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Reject/i }));
    fireEvent.click(within(screen.getByRole("dialog", { name: /Reject Edge action/i })).getByRole("button", { name: "Reject" }));
    expect(rejectMutate.mock.calls[0]?.[0]).toEqual({
      approvalRef: "edge_appr_1",
      reason: "Snapshot no longer matches policy intent",
    });
  });

  it("handles principal-bound 404 without leaking approval existence", () => {
    mockHooks([], new ApiError(404, "not found"));

    renderDrawer();

    expect(screen.queryByText("Approval not visible")).not.toBeNull();
    expect(screen.queryByText(/different requester, tenant, or terminal action/i)).not.toBeNull();
  });
});
