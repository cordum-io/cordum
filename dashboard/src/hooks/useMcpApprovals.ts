// React Query hooks for MCP per-tool approvals.
//
// These mirror the shape of the existing useApprovals/useApproveJob
// hooks but talk to the dedicated /mcp/approvals/* endpoints added in
// the gateway. Separated from useApprovals.ts so the MCP-specific
// query keys and mutation shapes stay self-contained — the job
// approval flow is complex enough that mixing the two concerns would
// make both harder to maintain.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { get, post, ApiError } from "../api/client";
import { logger } from "../lib/logger";
import { useToastStore } from "../state/toast";

// MCP approval lifecycle status — matches core/model ApprovalStatus.
export type McpApprovalStatus =
  | "pending"
  | "approved"
  | "rejected"
  | "expired"
  | "invalidated";

// MCP approval record shape from GET /api/v1/mcp/approvals/{id} and
// the list endpoint (see useMcpApprovals).
export interface McpApproval {
  id: string;
  tenant: string;
  agent_id: string;
  tool_name: string;
  args_hash: string;
  requester?: string;
  reason?: string;
  status: McpApprovalStatus;
  created_at: number;
  expires_at: number;
  resolved_at?: number;
  resolved_by?: string;
  decision?: string;
  consumed_at?: number;
  // Present only on the GET-by-id endpoint; lists do not include full args.
  args?: unknown;
}

const mcpApprovalsQueryKey = (status?: string) =>
  ["mcp-approvals", status ?? "all"] as const;

// useMcpApprovals polls the list endpoint. 5-second interval matches
// useApprovals so the two feeds stay visually in sync on the page.
// refetchIntervalInBackground is explicitly false so a backgrounded
// tab stops polling — the original cadence burned battery and API
// quota on tabs the operator was not actively watching.
export function useMcpApprovals(status?: McpApprovalStatus) {
  return useQuery<McpApproval[]>({
    queryKey: mcpApprovalsQueryKey(status),
    queryFn: async () => {
      const qs = status ? `?status=${encodeURIComponent(status)}` : "";
      const res = await get<{ items?: McpApproval[] }>(`/mcp/approvals${qs}`);
      return res.items ?? [];
    },
    staleTime: 5_000,
    refetchInterval: 5_000,
    refetchIntervalInBackground: false,
  });
}

// useMcpApproval fetches a single record — used by the args-review
// modal when the user clicks into a row.
export function useMcpApproval(id: string, enabled = true) {
  return useQuery<McpApproval>({
    queryKey: ["mcp-approval", id] as const,
    queryFn: async () => {
      return await get<McpApproval>(`/mcp/approvals/${encodeURIComponent(id)}`);
    },
    enabled: enabled && !!id,
    staleTime: 2_000,
  });
}

interface ResolveVars {
  id: string;
  reason?: string;
}

// useApproveMcp and useRejectMcp are separate hooks rather than a
// single resolve hook because each has distinct optimistic-update
// semantics and distinct toast copy.
export function useApproveMcp() {
  const qc = useQueryClient();
  const toast = useToastStore();
  return useMutation({
    mutationFn: async ({ id, reason }: ResolveVars) => {
      return await post<McpApproval>(`/mcp/approvals/${encodeURIComponent(id)}/approve`, {
        reason: reason ?? "",
      });
    },
    onSuccess: (rec) => {
      toast.addToast({ type: "success", title: `Approved MCP call for ${rec.tool_name}` });
      qc.invalidateQueries({ queryKey: ["mcp-approvals"] });
      qc.invalidateQueries({ queryKey: ["mcp-approval", rec.id] });
    },
    onError: (err: unknown) => {
      logger.warn("mcp approve failed", String(err));
      if (err instanceof ApiError && errorCode(err.body) === "self_approval_denied") {
        toast.addToast({
          type: "error",
          title: "Self-approval not permitted",
          description: "You cannot approve a call you issued yourself.",
        });
        return;
      }
      toast.addToast({ type: "error", title: "Approve failed" });
    },
  });
}

export function useRejectMcp() {
  const qc = useQueryClient();
  const toast = useToastStore();
  return useMutation({
    mutationFn: async ({ id, reason }: ResolveVars) => {
      return await post<McpApproval>(`/mcp/approvals/${encodeURIComponent(id)}/reject`, {
        reason: reason ?? "",
      });
    },
    onSuccess: (rec) => {
      toast.addToast({ type: "info", title: `Rejected MCP call for ${rec.tool_name}` });
      qc.invalidateQueries({ queryKey: ["mcp-approvals"] });
      qc.invalidateQueries({ queryKey: ["mcp-approval", rec.id] });
    },
    onError: (err: unknown) => {
      logger.warn("mcp reject failed", String(err));
      toast.addToast({ type: "error", title: "Reject failed" });
    },
  });
}

// errorCode safely pulls the `code` field from an ApiError body without
// trusting the shape. Gateway errors always carry {error, code, status};
// misbehaving servers may not — return "" when absent.
function errorCode(body: unknown): string {
  if (body && typeof body === "object" && "code" in body) {
    const c = (body as Record<string, unknown>).code;
    if (typeof c === "string") return c;
  }
  return "";
}

// Shortened args hash for tabular display — 8-char prefix is enough
// for operator disambiguation without eating screen width.
export function shortArgsHash(hash: string): string {
  if (!hash) return "";
  return hash.length > 10 ? `${hash.slice(0, 8)}…` : hash;
}
