import { useQuery, useMutation, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { get, post, ApiError } from "../api/client";
import { logger } from "../lib/logger";
import { queryKeys } from "../lib/queryKeys";
import { useToastStore } from "../state/toast";
import type {
  Approval,
  ApprovalContext,
  ApprovalConflictCode,
  ApprovalConflictPayload,
  ApiResponse,
} from "../api/types";
import { api } from "../lib/api";
import { mapApprovalItem, type BackendApprovalItem } from "../api/transform";

type ApprovalsSnapshot = { previous: [QueryKey, ApiResponse<Approval[]> | undefined][] };

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

// Max pages walked when looking up a single approval by id (task-f9ea8fe2).
// Backend has no GET /approvals/{id}; we walk the paginated list until found
// or until the cap. With page size 100 (server default), 10 pages = 1000
// approvals — generous in practice while bounding worst-case fetch cost.
export const APPROVAL_LOOKUP_MAX_PAGES = 10;

type ApprovalListPage = { items: BackendApprovalItem[]; next_cursor?: number | null };

/**
 * Walk the paginated /approvals list until an approval with the given id is
 * found, the list ends, or maxPages pages have been fetched. Throws
 * `approval not found` if exhausted without a hit. Pure function — fetcher is
 * injected so the queryFn can pass `get` and tests can pass a mock.
 */
async function lookupApprovalById(
  id: string,
  fetcher: (url: string) => Promise<ApprovalListPage>,
  maxPages: number = APPROVAL_LOOKUP_MAX_PAGES,
): Promise<Approval> {
  let cursor: number | null = null;
  for (let page = 0; page < maxPages; page++) {
    const url = cursor === null ? "/approvals" : `/approvals?cursor=${cursor}`;
    const res = await fetcher(url);
    const items = (res.items ?? [])
      .map(mapApprovalItem)
      .filter((v): v is Approval => !!v);
    const found = items.find((i) => i.id === id);
    if (found) return found;
    if (res.next_cursor === undefined || res.next_cursor === null) {
      throw new Error("approval not found");
    }
    cursor = res.next_cursor;
  }
  throw new Error(
    `approval lookup exceeded ${maxPages} pages without finding id`,
  );
}

export function useApproval(id: string) {
  const queryClient = useQueryClient();
  return useQuery<Approval>({
    queryKey: queryKeys.approvals.detail(id),
    queryFn: () =>
      lookupApprovalById(id, (url) =>
        get<ApprovalListPage>(url),
      ),
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

export function useApprovalContext(jobId: string) {
  return useQuery<ApprovalContext>({
    queryKey: queryKeys.approvals.context(jobId),
    queryFn: () => api.getApprovalContext(jobId),
    enabled: !!jobId,
    staleTime: 10_000,
  });
}

function filterApprovalsByStatus(items: Approval[], status?: string): Approval[] {
  if (!status?.trim()) return items;
  const normalized = status.trim().toLowerCase();
  return items.filter((item) => item.status.toLowerCase() === normalized);
}

function matchesApprovalIdentifier(approval: Approval, identifier: string): boolean {
  return approval.id === identifier || approval.jobId === identifier;
}

function removeApprovalFromList(
  old: ApiResponse<Approval[]> | undefined,
  identifier: string,
): ApiResponse<Approval[]> | undefined {
  if (!old?.items) return old;
  return {
    ...old,
    items: old.items.filter((approval) => !matchesApprovalIdentifier(approval, identifier)),
  };
}

function restoreApprovalToList(
  old: ApiResponse<Approval[]> | undefined,
  identifier: string,
  originalItem?: Approval,
): ApiResponse<Approval[]> | undefined {
  if (!old?.items || !originalItem) return old;
  if (old.items.some((approval) => matchesApprovalIdentifier(approval, identifier))) return old;
  return { ...old, items: [...old.items, originalItem] };
}

function findApprovalInSnapshot(
  snapshot: ApprovalsSnapshot | undefined,
  identifier: string,
): Approval | undefined {
  return snapshot?.previous
    ?.flatMap(([, data]) => data?.items ?? [])
    ?.find((approval) => matchesApprovalIdentifier(approval, identifier));
}

function getApprovalConflictCode(err: unknown): ApprovalConflictCode | undefined {
  if (!(err instanceof ApiError) || err.status !== 409) return undefined;
  const body = err.body as ApprovalConflictPayload | null | undefined;
  return body?.code;
}

function shouldKeepOptimisticRemoval(err: unknown): boolean {
  return getApprovalConflictCode(err) === "approval_already_resolved";
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
  jobId: string;
  comment?: string;
}

export function useApproveJob() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, ApproveInput, ApprovalsSnapshot>({
    mutationKey: ["approve-job"],
    mutationFn: ({ jobId, comment }) => {
      logger.info("approvals", "Approving job", { jobId });
      return post<void>(`/approvals/${encodeURIComponent(jobId)}/approve`, comment ? { note: comment } : undefined);
    },
    onMutate: async ({ jobId }) => {
      await queryClient.cancelQueries({ queryKey: queryKeys.approvals.all });
      const previous = queryClient.getQueriesData<ApiResponse<Approval[]>>({ queryKey: queryKeys.approvals.all });
      queryClient.setQueriesData<ApiResponse<Approval[]>>(
        { queryKey: queryKeys.approvals.all },
        (old) => removeApprovalFromList(old, jobId),
      );
      return { previous };
    },
    onSuccess: (_, { jobId }) => {
      logger.info("approvals", "Job approved", { jobId });
      useToastStore.getState().addToast({ type: "success", title: "Approved" });
    },
    onError: (err, { jobId }, context) => {
      if (!shouldKeepOptimisticRemoval(err)) {
        const originalItem = findApprovalInSnapshot(context, jobId);
        if (originalItem) {
          queryClient.setQueriesData<ApiResponse<Approval[]>>(
            { queryKey: queryKeys.approvals.all },
            (old) => restoreApprovalToList(old, jobId, originalItem),
          );
        }
      }
      const conflictCode = getApprovalConflictCode(err);
      if (conflictCode) {
        logger.info("approvals", "Approve conflicted", { jobId, conflictCode });
      } else {
        logger.error("approvals", "Approve failed", { jobId, error: err.message });
      }
    },
    onSettled: () => {
      invalidateApprovals(queryClient);
    },
  });
}


// Reject a job approval request (reason required)
interface RejectInput {
  jobId: string;
  reason: string;
  comment?: string;
}

export function useRejectJob() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, RejectInput, ApprovalsSnapshot>({
    mutationKey: ["reject-job"],
    mutationFn: ({ jobId, reason, comment }) => {
      logger.info("approvals", "Rejecting job", { jobId, reason });
      return post<void>(`/approvals/${encodeURIComponent(jobId)}/reject`, { reason, note: comment });
    },
    onMutate: async ({ jobId }) => {
      await queryClient.cancelQueries({ queryKey: queryKeys.approvals.all });
      const previous = queryClient.getQueriesData<ApiResponse<Approval[]>>({ queryKey: queryKeys.approvals.all });
      queryClient.setQueriesData<ApiResponse<Approval[]>>(
        { queryKey: queryKeys.approvals.all },
        (old) => removeApprovalFromList(old, jobId),
      );
      return { previous };
    },
    onSuccess: (_, { jobId }) => {
      logger.info("approvals", "Job rejected", { jobId });
      useToastStore.getState().addToast({ type: "success", title: "Rejected" });
    },
    onError: (err, { jobId }, context) => {
      if (!shouldKeepOptimisticRemoval(err)) {
        const originalItem = findApprovalInSnapshot(context, jobId);
        if (originalItem) {
          queryClient.setQueriesData<ApiResponse<Approval[]>>(
            { queryKey: queryKeys.approvals.all },
            (old) => restoreApprovalToList(old, jobId, originalItem),
          );
        }
      }
      const conflictCode = getApprovalConflictCode(err);
      if (conflictCode) {
        logger.info("approvals", "Reject conflicted", { jobId, conflictCode });
      } else {
        logger.error("approvals", "Reject failed", { jobId, error: err.message });
      }
    },
    onSettled: () => {
      invalidateApprovals(queryClient);
    },
  });
}


/** @internal exported for unit tests */
export const __approvalsInternal = {
  filterApprovalsByStatus,
  matchesApprovalIdentifier,
  removeApprovalFromList,
  restoreApprovalToList,
  findApprovalInSnapshot,
  getApprovalConflictCode,
  shouldKeepOptimisticRemoval,
  lookupApprovalById,
};
