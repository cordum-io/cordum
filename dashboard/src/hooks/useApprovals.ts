import { useQuery, useMutation, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { get, post, ApiError } from "../api/client";
import { logger } from "../lib/logger";
import { useToastStore } from "../state/toast";
import type { Approval, ApprovalHistoryEntry, ApiResponse } from "../api/types";
import { mapApprovalItem, type BackendApprovalItem, type BackendPolicyAuditEntry } from "../api/transform";

type ApprovalsSnapshot = { previous: [QueryKey, ApiResponse<Approval[]> | undefined][] };

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
    refetchInterval: 5_000,
  });
}

export function useApproval(id: string) {
  const queryClient = useQueryClient();
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
    placeholderData: () => {
      const cached = queryClient.getQueryData<ApiResponse<Approval[]>>(["approvals", "all"]);
      return cached?.items?.find((i) => i.id === id);
    },
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
  return useQuery<ApiResponse<ApprovalHistoryEntry[]>>({
    queryKey: ["approvals", "history", filters],
    queryFn: async () => {
      const qs = buildHistoryParams(filters);
      const res = await get<{ items: BackendPolicyAuditEntry[] }>(
        `/policy/audit${qs}`,
      );
      const items = (res.items ?? [])
        .filter(
          (e) => e.action === "approve" || e.action === "reject",
        )
        .map((e): ApprovalHistoryEntry => {
          // Try to extract extra fields from snapshot_after
          let topic: string | undefined;
          let workflowId: string | undefined;
          let waitDurationMs: number | undefined;
          if (e.snapshot_after) {
            try {
              const snap = typeof e.snapshot_after === "string"
                ? JSON.parse(e.snapshot_after)
                : e.snapshot_after;
              topic = snap.topic;
              workflowId = snap.workflow_id;
              if (snap.requested_at && e.created_at) {
                waitDurationMs = new Date(e.created_at).getTime() - new Date(snap.requested_at).getTime();
              }
            } catch {
              // ignore parse errors
            }
          }
          return {
            id: e.id,
            action: e.action as "approve" | "reject",
            jobId: e.resource_id || "",
            actor: e.actor_id || e.role || "unknown",
            timestamp: e.created_at || "",
            reason: e.message,
            policyRule: e.resource_type === "policy_rule" ? e.resource_id : undefined,
            bundleIds: e.bundle_ids,
            topic,
            workflowId,
            waitDurationMs,
          };
        });
      return { items };
    },
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
  return useMutation<void, Error, ApproveInput, ApprovalsSnapshot>({
    mutationFn: ({ id, comment }) => {
      logger.info("approvals", "Approving job", { id });
      return post<void>(`/approvals/${id}/approve`, comment ? { note: comment } : undefined);
    },
    onMutate: async ({ id }) => {
      await queryClient.cancelQueries({ queryKey: ["approvals"] });
      const previous = queryClient.getQueriesData<ApiResponse<Approval[]>>({ queryKey: ["approvals"] });
      queryClient.setQueriesData<ApiResponse<Approval[]>>(
        { queryKey: ["approvals"] },
        (old) => {
          if (!old?.items) return old;
          return { ...old, items: old.items.filter((a) => a.id !== id) };
        },
      );
      return { previous };
    },
    onSuccess: (_, { id }) => {
      logger.info("approvals", "Job approved", { id });
      useToastStore.getState().addToast({ type: "success", title: "Approved" });
    },
    onError: (err, { id }, context) => {
      if (context?.previous) {
        for (const [key, data] of context.previous) {
          queryClient.setQueryData(key, data);
        }
      }
      logger.error("approvals", "Approve failed", { id, error: err.message });
      const desc = err instanceof ApiError && err.status === 409
        ? "Approval state changed \u2014 refresh and try again"
        : err.message;
      useToastStore.getState().addToast({ type: "error", title: "Approval failed", description: desc });
    },
    onSettled: () => {
      invalidateApprovals(queryClient);
    },
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
  return useMutation<void, Error, RejectInput, ApprovalsSnapshot>({
    mutationFn: ({ id, reason, comment }) => {
      logger.info("approvals", "Rejecting job", { id, reason });
      return post<void>(`/approvals/${id}/reject`, { reason, note: comment });
    },
    onMutate: async ({ id }) => {
      await queryClient.cancelQueries({ queryKey: ["approvals"] });
      const previous = queryClient.getQueriesData<ApiResponse<Approval[]>>({ queryKey: ["approvals"] });
      queryClient.setQueriesData<ApiResponse<Approval[]>>(
        { queryKey: ["approvals"] },
        (old) => {
          if (!old?.items) return old;
          return { ...old, items: old.items.filter((a) => a.id !== id) };
        },
      );
      return { previous };
    },
    onSuccess: (_, { id }) => {
      logger.info("approvals", "Job rejected", { id });
      useToastStore.getState().addToast({ type: "success", title: "Rejected" });
    },
    onError: (err, { id }, context) => {
      if (context?.previous) {
        for (const [key, data] of context.previous) {
          queryClient.setQueryData(key, data);
        }
      }
      logger.error("approvals", "Reject failed", { id, error: err.message });
      const desc = err instanceof ApiError && err.status === 409
        ? "Approval state changed \u2014 refresh and try again"
        : err.message;
      useToastStore.getState().addToast({ type: "error", title: "Rejection failed", description: desc });
    },
    onSettled: () => {
      invalidateApprovals(queryClient);
    },
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
      logger.info("approvals", "Approving step", { workflowId, runId, stepId });
      return post<void>(
        `/workflows/${workflowId}/runs/${runId}/steps/${stepId}/approve`,
        { approved: approved ?? true },
      );
    },
    onSuccess: (_, { stepId }) => {
      logger.info("approvals", "Step approved", { stepId });
      useToastStore.getState().addToast({ type: "success", title: "Step approved" });
      invalidateApprovals(queryClient);
    },
    onError: (err, { stepId }) => {
      logger.error("approvals", "Step approve failed", { stepId, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Step approval failed", description: err.message });
      if (err instanceof ApiError && err.status === 409) {
        invalidateApprovals(queryClient);
      }
    },
  });
}
