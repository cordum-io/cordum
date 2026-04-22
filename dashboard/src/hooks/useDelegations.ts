import type { InfiniteData } from "@tanstack/react-query";
import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { get, post } from "../api/client";
import {
  mapDelegationListResponse,
  type BackendDelegationListResponse,
} from "../api/transform";
import type {
  DelegationListResponse,
  DelegationStatus,
  RevokeDelegationResult,
} from "../api/types";

export interface DelegationFilters {
  status?: DelegationStatus;
  scope?: string;
  beforeExpiry?: string;
  sinceIssued?: string;
  untilIssued?: string;
  limit?: number;
}

export interface UseDelegationsOptions {
  enabled?: boolean;
  staleTimeMs?: number;
}

export interface RevokeDelegationInput {
  jti: string;
  reason?: string;
  cascade?: boolean;
}

interface BackendRevokeDelegationResponse {
  jti?: string;
  cascaded_count?: number;
}

type DelegationInfiniteData = InfiniteData<DelegationListResponse, string | undefined>;

export const delegationQueryKeys = {
  all: (filters: DelegationFilters = {}) => ["delegations", "all", { ...filters }] as const,
  agent: (agentId: string | undefined, filters: DelegationFilters = {}) =>
    ["delegations", "agent", agentId ?? "", { ...filters }] as const,
};

export function useAllDelegations(
  filters: DelegationFilters = {},
  options: UseDelegationsOptions = {},
) {
  return useInfiniteQuery<DelegationListResponse, Error>({
    queryKey: delegationQueryKeys.all(filters),
    queryFn: async ({ pageParam }) => {
      const cursor = typeof pageParam === "string" ? pageParam : undefined;
      const raw = await get<BackendDelegationListResponse>(
        `/delegations${buildDelegationQuery(filters, cursor)}`,
      );
      return mapDelegationListResponse(raw);
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
    staleTime: options.staleTimeMs ?? 30_000,
    enabled: options.enabled ?? true,
  });
}

export function useAgentDelegations(
  agentId: string | undefined,
  filters: DelegationFilters = {},
  options: UseDelegationsOptions = {},
) {
  return useInfiniteQuery<DelegationListResponse, Error>({
    queryKey: delegationQueryKeys.agent(agentId, filters),
    queryFn: async ({ pageParam }) => {
      const cursor = typeof pageParam === "string" ? pageParam : undefined;
      const raw = await get<BackendDelegationListResponse>(
        `/agents/${encodeURIComponent(agentId!)}/delegations${buildDelegationQuery(filters, cursor)}`,
      );
      return mapDelegationListResponse(raw);
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
    staleTime: options.staleTimeMs ?? 30_000,
    enabled: Boolean(agentId) && (options.enabled ?? true),
  });
}

export function useRevokeDelegation() {
  const queryClient = useQueryClient();
  return useMutation<
    RevokeDelegationResult,
    Error,
    RevokeDelegationInput,
    { snapshots: Array<[readonly unknown[], DelegationInfiniteData | undefined]> }
  >({
    mutationFn: async (input) => {
      const raw = await post<BackendRevokeDelegationResponse>("/agents/revoke-delegation", {
        jti: input.jti,
        reason: input.reason,
        cascade: input.cascade,
      });
      return {
        jti: raw.jti ?? input.jti,
        cascadedCount:
          typeof raw.cascaded_count === "number" ? raw.cascaded_count : 0,
      };
    },
    onMutate: async (input) => {
      await queryClient.cancelQueries({ queryKey: ["delegations"] });
      const snapshots = queryClient.getQueriesData<DelegationInfiniteData>({
        queryKey: ["delegations"],
      });
      const revokedAt = new Date().toISOString();
      for (const [key, data] of snapshots) {
        const optimistic = applyOptimisticRevocation(
          data,
          input.jti,
          input.reason,
          revokedAt,
        );
        if (optimistic !== data) {
          queryClient.setQueryData(key, optimistic);
        }
      }
      return { snapshots };
    },
    onError: (_error, _input, context) => {
      for (const [key, data] of context?.snapshots ?? []) {
        queryClient.setQueryData(key, data);
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["delegations", "all"] });
      queryClient.invalidateQueries({ queryKey: ["delegations", "agent"] });
    },
  });
}

function buildDelegationQuery(
  filters: DelegationFilters,
  cursor?: string,
): string {
  const params = new URLSearchParams();
  if (filters.status) params.set("status", filters.status);
  if (filters.scope) params.set("scope", filters.scope);
  if (filters.beforeExpiry) params.set("before_expiry", filters.beforeExpiry);
  if (filters.sinceIssued) params.set("since_issued", filters.sinceIssued);
  if (filters.untilIssued) params.set("until_issued", filters.untilIssued);
  if (typeof filters.limit === "number") params.set("limit", String(filters.limit));
  if (cursor) params.set("cursor", cursor);
  const query = params.toString();
  return query ? `?${query}` : "";
}

function applyOptimisticRevocation(
  data: DelegationInfiniteData | undefined,
  jti: string,
  reason: string | undefined,
  revokedAt: string,
): DelegationInfiniteData | undefined {
  if (!data) return data;
  let touched = false;
  const pages = data.pages.map((page) => {
    let pageTouched = false;
    const items = page.items.map((item) => {
      if (item.jti !== jti) return item;
      pageTouched = true;
      touched = true;
      return {
        ...item,
        revoked: true,
        revokedAt: item.revokedAt ?? revokedAt,
        revokedReason: reason ?? item.revokedReason,
      };
    });
    return pageTouched ? { ...page, items } : page;
  });
  return touched ? { ...data, pages } : data;
}

/** @internal for tests */
export const __delegationsInternal = {
  buildDelegationQuery,
  applyOptimisticRevocation,
};
