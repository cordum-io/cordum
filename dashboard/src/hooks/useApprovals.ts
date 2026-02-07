import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post } from "../api/client";
import type { Approval, ApiResponse } from "../api/types";
import { mapApprovalItem, type BackendApprovalItem } from "../api/transform";

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

export function useApprovals(status?: string) {
  return useQuery<ApiResponse<Approval[]>>({
    queryKey: ["approvals", status ?? "all"],
    queryFn: async () => {
      const res = await get<{ items: BackendApprovalItem[]; next_cursor?: number | null }>(
        `/approvals`,
      );
      const items = (res.items ?? [])
        .map(mapApprovalItem)
        .filter((v): v is Approval => !!v);
      return { items, next_cursor: res.next_cursor ?? null };
    },
    staleTime: 5_000,
    refetchInterval: 10_000,
  });
}

export function useApproval(id: string) {
  return useQuery<Approval>({
    queryKey: ["approval", id],
    queryFn: async () => {
      const res = await get<{ items: BackendApprovalItem[] }>(`/approvals`);
      const items = (res.items ?? [])
        .map(mapApprovalItem)
        .filter((v): v is Approval => !!v);
      const found = items.find((i) => i.id === id);
      if (!found) {
        throw new Error("approval not found");
      }
      return found;
    },
    enabled: !!id,
    staleTime: 5_000,
  });
}

// ---------------------------------------------------------------------------
// History query
// ---------------------------------------------------------------------------

export interface ApprovalHistoryFilters {
  page?: number;
  perPage?: number;
  sort?: string;
}

function buildHistoryParams(filters: ApprovalHistoryFilters): string {
  const params = new URLSearchParams();
  if (filters.page !== undefined) params.set("page", String(filters.page));
  if (filters.perPage !== undefined) params.set("perPage", String(filters.perPage));
  if (filters.sort) params.set("sort", filters.sort);
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

export function useApprovalHistory(filters: ApprovalHistoryFilters = {}) {
  return useQuery<ApiResponse<Approval[]>>({
    queryKey: ["approvals", "history", filters],
    queryFn: async () => ({ items: [] }),
    staleTime: 60_000,
  });
}

// ---------------------------------------------------------------------------
// Mutations
// ---------------------------------------------------------------------------

const APPROVALS_KEYS = [["approvals"], ["approvals", "nav"]] as const;

function invalidateApprovals(queryClient: ReturnType<typeof useQueryClient>) {
  for (const key of APPROVALS_KEYS) {
    queryClient.invalidateQueries({ queryKey: [...key] });
  }
}

// Approve a job approval request
interface ApproveInput {
  id: string;
  comment?: string;
}

export function useApproveJob() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, ApproveInput>({
    mutationFn: ({ id, comment }) =>
      post<void>(`/approvals/${id}/approve`, comment ? { note: comment } : undefined),
    onSuccess: () => invalidateApprovals(queryClient),
  });
}

// Keep old name as alias for backwards compat
export const useApproveApproval = useApproveJob;

// Reject a job approval request (reason required)
interface RejectInput {
  id: string;
  reason: string;
  comment?: string;
}

export function useRejectJob() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, RejectInput>({
    mutationFn: ({ id, reason, comment }) =>
      post<void>(`/approvals/${id}/reject`, { reason, note: comment }),
    onSuccess: () => invalidateApprovals(queryClient),
  });
}

// Keep old name as alias for backwards compat
export const useRejectApproval = useRejectJob;

// Approve a workflow step
interface ApproveStepInput {
  workflowId: string;
  runId: string;
  stepId: string;
  approved?: boolean;
}

export function useApproveStep() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, ApproveStepInput>({
    mutationFn: ({ workflowId, runId, stepId, approved }) => {
      if (!workflowId || !runId || !stepId) {
        return Promise.reject(new Error("workflowId, runId, and stepId are required"));
      }
      return post<void>(
        `/workflows/${workflowId}/runs/${runId}/steps/${stepId}/approve`,
        { approved: approved ?? true },
      );
    },
    onSuccess: () => invalidateApprovals(queryClient),
  });
}
