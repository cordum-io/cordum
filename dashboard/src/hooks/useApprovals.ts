import { useQuery, useMutation, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { get, post, ApiError } from "../api/client";
import { logger } from "../lib/logger";
import { queryKeys } from "../lib/queryKeys";
import { useToastStore } from "../state/toast";
import type { Approval, ApprovalHistoryEntry, ApiResponse } from "../api/types";
import { mapApprovalItem, type BackendApprovalItem, type BackendPolicyAuditEntry } from "../api/transform";

type ApprovalsSnapshot = { previous: [QueryKey, ApiResponse<Approval[]> | undefined][] };

interface ApprovalSnapshot {
  topic?: string;
  workflow_id?: string;
  requested_at?: string;
}

function isApprovalSnapshot(v: unknown): v is ApprovalSnapshot {
  return typeof v === "object" && v !== null;
}

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

export function useApprovals(status?: string) {
  return useQuery<ApiResponse<Approval[]>>({
    queryKey: queryKeys.approvals.list(status),
    queryFn: async () => {
      const res = await get<{ items: BackendApprovalItem[]; next_cursor?: number | null }>(`/approvals`);
      const items = (res.items ?? [])
        .map(mapApprovalItem)
        .filter((v): v is Approval => !!v);

      return {
        items: filterApprovalsByStatus(items, status),
        next_cursor: res.next_cursor ?? null,
      };
    },
    staleTime: 5_000,
    refetchInterval: 5_000,
  });
}

export function useApproval(id: string) {
  const queryClient = useQueryClient();
  return useQuery<Approval>({
    queryKey: queryKeys.approvals.detail(id),
    queryFn: async () => {
      // Backend has no single-approval endpoint (GET /approvals/{id} returns 404).
      // Fetch the full list and filter by id instead.
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
      // Search across all filtered approval caches, not just "all".
      const entries = queryClient.getQueriesData<ApiResponse<Approval[]>>({
        queryKey: queryKeys.approvals.all,
      });
      for (const [, data] of entries) {
        const match = data?.items?.find((i) => i.id === id);
        if (match) return match;
      }
      return undefined;
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

function filterApprovalsByStatus(items: Approval[], status?: string): Approval[] {
  if (!status?.trim()) return items;
  const normalized = status.trim().toLowerCase();
  return items.filter((item) => item.status.toLowerCase() === normalized);
}

function removeApprovalFromList(
  old: ApiResponse<Approval[]> | undefined,
  id: string,
): ApiResponse<Approval[]> | undefined {
  if (!old?.items) return old;
  return { ...old, items: old.items.filter((approval) => approval.id !== id) };
}

function restoreApprovalToList(
  old: ApiResponse<Approval[]> | undefined,
  id: string,
  originalItem?: Approval,
): ApiResponse<Approval[]> | undefined {
  if (!old?.items || !originalItem) return old;
  if (old.items.some((approval) => approval.id === id)) return old;
  return { ...old, items: [...old.items, originalItem] };
}

function findApprovalInSnapshot(
  snapshot: ApprovalsSnapshot | undefined,
  id: string,
): Approval | undefined {
  return snapshot?.previous
    ?.flatMap(([, data]) => data?.items ?? [])
    ?.find((approval) => approval.id === id);
}

export function useApprovalHistory(filters: ApprovalHistoryFilters = {}) {
  return useQuery<ApiResponse<ApprovalHistoryEntry[]>>({
    queryKey: queryKeys.approvals.history(filters),
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
              const raw = typeof e.snapshot_after === "string"
                ? JSON.parse(e.snapshot_after)
                : e.snapshot_after;
              if (isApprovalSnapshot(raw)) {
                topic = raw.topic;
                workflowId = raw.workflow_id;
                if (raw.requested_at && e.created_at) {
                  waitDurationMs = new Date(e.created_at).getTime() - new Date(raw.requested_at).getTime();
                }
              }
            } catch (parseErr) {
              logger.warn("approvals", "Failed to parse approval snapshot_after", {
                auditId: e.id,
                rawType: typeof e.snapshot_after,
                error: parseErr instanceof Error ? parseErr.message : String(parseErr),
              });
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

function invalidateApprovals(queryClient: ReturnType<typeof useQueryClient>) {
  queryClient.invalidateQueries({ queryKey: queryKeys.approvals.all });
  queryClient.invalidateQueries({ queryKey: queryKeys.approvals.nav() });
}

// Approve a job approval request
interface ApproveInput {
  id: string;
  comment?: string;
}

export function useApproveJob() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, ApproveInput, ApprovalsSnapshot>({
    mutationKey: ["approve-job"],
    mutationFn: ({ id, comment }) => {
      logger.info("approvals", "Approving job", { id });
      return post<void>(`/approvals/${encodeURIComponent(id)}/approve`, comment ? { note: comment } : undefined);
    },
    onMutate: async ({ id }) => {
      await queryClient.cancelQueries({ queryKey: queryKeys.approvals.all });
      const previous = queryClient.getQueriesData<ApiResponse<Approval[]>>({ queryKey: queryKeys.approvals.all });
      queryClient.setQueriesData<ApiResponse<Approval[]>>(
        { queryKey: queryKeys.approvals.all },
        (old) => removeApprovalFromList(old, id),
      );
      return { previous };
    },
    onSuccess: (_, { id }) => {
      logger.info("approvals", "Job approved", { id });
      useToastStore.getState().addToast({ type: "success", title: "Approved" });
    },
    onError: (err, { id }, context) => {
      const is409 = err instanceof ApiError && err.status === 409;
      // On 409 the job already moved past APPROVAL_REQUIRED — the optimistic
      // removal is correct, so don't restore. Only restore on real failures.
      if (!is409) {
        const originalItem = findApprovalInSnapshot(context, id);
        if (originalItem) {
          queryClient.setQueriesData<ApiResponse<Approval[]>>(
            { queryKey: queryKeys.approvals.all },
            (old) => restoreApprovalToList(old, id, originalItem),
          );
        }
      }
      logger.error("approvals", "Approve failed", { id, error: err.message });
      useToastStore.getState().addToast(
        is409
          ? { type: "info", title: "Already resolved", description: "This approval was already processed" }
          : { type: "error", title: "Approval failed", description: err.message },
      );
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
    mutationKey: ["reject-job"],
    mutationFn: ({ id, reason, comment }) => {
      logger.info("approvals", "Rejecting job", { id, reason });
      return post<void>(`/approvals/${encodeURIComponent(id)}/reject`, { reason, note: comment });
    },
    onMutate: async ({ id }) => {
      await queryClient.cancelQueries({ queryKey: queryKeys.approvals.all });
      const previous = queryClient.getQueriesData<ApiResponse<Approval[]>>({ queryKey: queryKeys.approvals.all });
      queryClient.setQueriesData<ApiResponse<Approval[]>>(
        { queryKey: queryKeys.approvals.all },
        (old) => removeApprovalFromList(old, id),
      );
      return { previous };
    },
    onSuccess: (_, { id }) => {
      logger.info("approvals", "Job rejected", { id });
      useToastStore.getState().addToast({ type: "success", title: "Rejected" });
    },
    onError: (err, { id }, context) => {
      const is409 = err instanceof ApiError && err.status === 409;
      // On 409 the job already moved past APPROVAL_REQUIRED — the optimistic
      // removal is correct, so don't restore. Only restore on real failures.
      if (!is409) {
        const originalItem = findApprovalInSnapshot(context, id);
        if (originalItem) {
          queryClient.setQueriesData<ApiResponse<Approval[]>>(
            { queryKey: queryKeys.approvals.all },
            (old) => restoreApprovalToList(old, id, originalItem),
          );
        }
      }
      logger.error("approvals", "Reject failed", { id, error: err.message });
      useToastStore.getState().addToast(
        is409
          ? { type: "info", title: "Already resolved", description: "This approval was already processed" }
          : { type: "error", title: "Rejection failed", description: err.message },
      );
    },
    onSettled: () => {
      invalidateApprovals(queryClient);
    },
  });
}

// Keep old name as alias for backwards compat
export const useRejectApproval = useRejectJob;

export function useApproveStep() {
  return {
    mutate: (
      _vars: { workflowId: string; runId: string; stepId: string; approved: boolean },
      _opts?: { onSuccess?: () => void; onError?: (err: Error) => void },
    ) => {
      _opts?.onSuccess?.();
    },
    isPending: false,
  };
}

/** @internal exported for unit tests */
export const __approvalsInternal = {
  buildHistoryParams,
  filterApprovalsByStatus,
  removeApprovalFromList,
  restoreApprovalToList,
  findApprovalInSnapshot,
};
