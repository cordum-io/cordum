import { useQuery, useMutation, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { get, post, del } from "../api/client";
import { logger } from "../lib/logger";
import { queryKeys } from "../lib/queryKeys";
import { useToastStore } from "../state/toast";
import type { DLQEntry, ApiResponse } from "../api/types";
import { mapDLQEntry, type BackendDLQEntry } from "../api/transform";

// ---------------------------------------------------------------------------
// Filters
// ---------------------------------------------------------------------------

export interface DLQFilters {
  limit?: number;
  cursor?: number;
}

function buildParams(filters: DLQFilters): string {
  const params = new URLSearchParams();
  if (filters.limit !== undefined) params.set("limit", String(filters.limit));
  if (filters.cursor !== undefined && filters.cursor > 0) {
    params.set("cursor", String(filters.cursor));
  }
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

export function useDLQ(filters: DLQFilters = {}) {
  return useQuery<ApiResponse<DLQEntry[]>>({
    queryKey: queryKeys.dlq.list(filters),
    queryFn: async () => {
      const res = await get<{ items: BackendDLQEntry[]; next_cursor?: number | null }>(
        `/dlq/page${buildParams(filters)}`,
      );
      return {
        items: (res.items ?? []).map(mapDLQEntry),
        next_cursor: res.next_cursor ?? null,
      };
    },
    staleTime: 10_000,
  });
}

// ---------------------------------------------------------------------------
// Mutations — single
// ---------------------------------------------------------------------------

interface RetryInput {
  id: string;
}

export function useRetryDLQ() {
  const queryClient = useQueryClient();
  type DLQSnapshot = { previous: [QueryKey, ApiResponse<DLQEntry[]> | undefined][] };
  return useMutation<void, Error, RetryInput, DLQSnapshot>({
    mutationFn: ({ id }) => {
      logger.info("dlq", "Retrying DLQ entry", { id });
      return post<void>(`/dlq/${encodeURIComponent(id)}/retry`);
    },
    onMutate: async ({ id }) => {
      await queryClient.cancelQueries({ queryKey: queryKeys.dlq.all });
      const previous = queryClient.getQueriesData<ApiResponse<DLQEntry[]>>({ queryKey: queryKeys.dlq.all });
      queryClient.setQueriesData<ApiResponse<DLQEntry[]>>(
        { queryKey: queryKeys.dlq.all },
        (old) => {
          if (!old?.items) return old;
          return { ...old, items: old.items.filter((e) => e.id !== id) };
        },
      );
      return { previous };
    },
    onSuccess: (_, { id }) => {
      logger.info("dlq", "DLQ entry retried", { id });
      useToastStore.getState().addToast({ type: "success", title: "Retrying entry" });
    },
    onError: (err, { id }, context) => {
      if (context?.previous) {
        for (const [key, data] of context.previous) {
          queryClient.setQueryData(key, data);
        }
      }
      logger.error("dlq", "DLQ retry failed", { id, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to retry entry", description: err.message });
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.dlq.all });
    },
  });
}

export function useDeleteDLQ() {
  const queryClient = useQueryClient();
  type DLQSnapshot = { previous: [QueryKey, ApiResponse<DLQEntry[]> | undefined][] };
  return useMutation<void, Error, string, DLQSnapshot>({
    mutationFn: (id) => {
      logger.info("dlq", "Deleting DLQ entry", { id });
      return del(`/dlq/${encodeURIComponent(id)}`);
    },
    onMutate: async (id) => {
      await queryClient.cancelQueries({ queryKey: queryKeys.dlq.all });
      const previous = queryClient.getQueriesData<ApiResponse<DLQEntry[]>>({ queryKey: queryKeys.dlq.all });
      queryClient.setQueriesData<ApiResponse<DLQEntry[]>>(
        { queryKey: queryKeys.dlq.all },
        (old) => {
          if (!old?.items) return old;
          return { ...old, items: old.items.filter((e) => e.id !== id) };
        },
      );
      return { previous };
    },
    onSuccess: (_, id) => {
      logger.info("dlq", "DLQ entry deleted", { id });
      useToastStore.getState().addToast({ type: "success", title: "Entry deleted" });
    },
    onError: (err, id, context) => {
      if (context?.previous) {
        for (const [key, data] of context.previous) {
          queryClient.setQueryData(key, data);
        }
      }
      logger.error("dlq", "DLQ delete failed", { id, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to delete entry", description: err.message });
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.dlq.all });
    },
  });
}

// ---------------------------------------------------------------------------
// Mutations — bulk
// ---------------------------------------------------------------------------

export function useBulkRetryDLQ() {
  const queryClient = useQueryClient();
  return useMutation<PromiseSettledResult<void>[], Error, string[]>({
    mutationFn: (ids) => {
      logger.info("dlq", "Bulk retrying DLQ entries", { count: ids.length });
      return Promise.allSettled(ids.map((id) => post<void>(`/dlq/${encodeURIComponent(id)}/retry`)));
    },
    onSuccess: (results, ids) => {
      const failed = results.filter((r) => r.status === "rejected").length;
      if (failed > 0) {
        useToastStore.getState().addToast({ type: "warning", title: `Retried ${ids.length - failed}/${ids.length} — ${failed} failed` });
      } else {
        useToastStore.getState().addToast({ type: "success", title: `Retrying ${ids.length} items` });
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.dlq.all });
    },
    onError: (err) => {
      useToastStore.getState().addToast({ type: "error", title: "Bulk retry failed", description: err.message });
    },
  });
}

export function useBulkDeleteDLQ() {
  const queryClient = useQueryClient();
  return useMutation<PromiseSettledResult<void>[], Error, string[]>({
    mutationFn: (ids) => {
      logger.info("dlq", "Bulk deleting DLQ entries", { count: ids.length });
      return Promise.allSettled(ids.map((id) => del(`/dlq/${encodeURIComponent(id)}`)));
    },
    onSuccess: (results, ids) => {
      const failed = results.filter((r) => r.status === "rejected").length;
      if (failed > 0) {
        useToastStore.getState().addToast({ type: "warning", title: `Purged ${ids.length - failed}/${ids.length} — ${failed} failed` });
      } else {
        useToastStore.getState().addToast({ type: "success", title: `Purged ${ids.length} items` });
      }
      queryClient.invalidateQueries({ queryKey: queryKeys.dlq.all });
    },
    onError: (err) => {
      useToastStore.getState().addToast({ type: "error", title: "Bulk purge failed", description: err.message });
    },
  });
}

/** @internal exported for unit tests */
export const __dlqInternal = {
  buildParams,
};
