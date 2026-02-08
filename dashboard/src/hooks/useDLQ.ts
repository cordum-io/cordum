import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post, del } from "../api/client";
import { logger } from "../lib/logger";
import type { DLQEntry, ApiResponse } from "../api/types";
import { mapDLQEntry, type BackendDLQEntry } from "../api/transform";

// ---------------------------------------------------------------------------
// Filters
// ---------------------------------------------------------------------------

export interface DLQFilters {
  limit?: number;
  cursor?: number;
  topic?: string;
  since?: string;
}

function buildParams(filters: DLQFilters): string {
  const params = new URLSearchParams();
  if (filters.limit !== undefined) params.set("limit", String(filters.limit));
  if (filters.cursor !== undefined && filters.cursor > 0) {
    params.set("cursor", String(filters.cursor));
  }
  if (filters.topic) params.set("topic", filters.topic);
  if (filters.since) params.set("since", filters.since);
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

export function useDLQ(filters: DLQFilters = {}) {
  return useQuery<ApiResponse<DLQEntry[]>>({
    queryKey: ["dlq", filters],
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
  return useMutation<void, Error, RetryInput>({
    mutationFn: ({ id }) => {
      logger.info("dlq", "Retrying DLQ entry", { id });
      return post<void>(`/dlq/${id}/retry`);
    },
    onSuccess: (_, { id }) => {
      logger.info("dlq", "DLQ entry retried", { id });
      queryClient.invalidateQueries({ queryKey: ["dlq"] });
    },
    onError: (err, { id }) => {
      logger.error("dlq", "DLQ retry failed", { id, error: err.message });
    },
  });
}

export function useDeleteDLQ() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (id) => {
      logger.info("dlq", "Deleting DLQ entry", { id });
      return del(`/dlq/${id}`);
    },
    onSuccess: (_, id) => {
      logger.info("dlq", "DLQ entry deleted", { id });
      queryClient.invalidateQueries({ queryKey: ["dlq"] });
    },
    onError: (err, id) => {
      logger.error("dlq", "DLQ delete failed", { id, error: err.message });
    },
  });
}
