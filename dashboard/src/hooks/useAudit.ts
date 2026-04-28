import { useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { get } from "../api/client";
import type { AuditEntry, ApiResponse } from "../api/types";
import { mapPolicyAuditEntry, type BackendPolicyAuditEntry } from "../api/transform";
import { queryKeys } from "../lib/queryKeys";

// ---------------------------------------------------------------------------
// Filters
// ---------------------------------------------------------------------------

export interface AuditFilters {
  eventType?: string[];
  actor?: string;
  resourceType?: string;
  resourceId?: string;
  severity?: string[];
  outcome?: string[];
  timeRange?: string;
  from?: string;
  to?: string;
  search?: string;
  page?: number;
  perPage?: number;
  sort?: string;
}

// ---------------------------------------------------------------------------
// Time-range helpers
// ---------------------------------------------------------------------------

const TIME_RANGE_MS: Record<string, number> = {
  "1h": 60 * 60 * 1000,
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
  "30d": 30 * 24 * 60 * 60 * 1000,
  "90d": 90 * 24 * 60 * 60 * 1000,
};

// ---------------------------------------------------------------------------
// Client-side filter + sort
// ---------------------------------------------------------------------------

function applyFilters(entries: AuditEntry[], f: AuditFilters): AuditEntry[] {
  let result = entries;

  if (f.eventType?.length) {
    const set = new Set(f.eventType);
    result = result.filter((e) => set.has(e.eventType));
  }
  if (f.actor) {
    const lower = f.actor.toLowerCase();
    result = result.filter((e) => e.actor.toLowerCase().includes(lower));
  }
  if (f.resourceType) {
    result = result.filter((e) => e.resourceType === f.resourceType);
  }
  if (f.resourceId) {
    result = result.filter((e) => e.resourceId === f.resourceId);
  }
  if (f.severity?.length) {
    const set = new Set(f.severity);
    result = result.filter((e) => e.severity && set.has(e.severity));
  }
  if (f.outcome?.length) {
    const set = new Set(f.outcome);
    result = result.filter((e) => set.has(e.action.toLowerCase()));
  }
  if (f.timeRange === "custom" && (f.from || f.to)) {
    const fromMs = f.from ? new Date(f.from).getTime() : 0;
    const toMs = f.to ? new Date(f.to).getTime() : Infinity;
    result = result.filter((e) => {
      const t = new Date(e.timestamp).getTime();
      return t >= fromMs && t <= toMs;
    });
  } else if (f.timeRange && TIME_RANGE_MS[f.timeRange]) {
    const cutoff = Date.now() - TIME_RANGE_MS[f.timeRange];
    result = result.filter((e) => new Date(e.timestamp).getTime() >= cutoff);
  }
  if (f.search) {
    const lower = f.search.toLowerCase();
    result = result.filter(
      (e) =>
        e.action.toLowerCase().includes(lower) ||
        e.actor.toLowerCase().includes(lower) ||
        e.message.toLowerCase().includes(lower) ||
        e.resourceType.toLowerCase().includes(lower) ||
        e.resourceId.toLowerCase().includes(lower) ||
        (e.payload && JSON.stringify(e.payload).toLowerCase().includes(lower)),
    );
  }

  return result;
}

function applySort(entries: AuditEntry[], sort?: string): AuditEntry[] {
  const effectiveSort = sort || "time-desc";
  const sorted = [...entries];
  switch (effectiveSort) {
    case "time-asc":
      sorted.sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime());
      break;
    case "time-desc":
      sorted.sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime());
      break;
    case "action-asc":
      sorted.sort((a, b) => (a.eventType || a.action).localeCompare(b.eventType || b.action));
      break;
    case "action-desc":
      sorted.sort((a, b) => (b.eventType || b.action).localeCompare(a.eventType || a.action));
      break;
    default:
      break;
  }
  return sorted;
}

// ---------------------------------------------------------------------------
// Hooks
// ---------------------------------------------------------------------------

export function useAuditLog(filters: AuditFilters = {}) {
  const query = useQuery<ApiResponse<AuditEntry[]>>({
    queryKey: queryKeys.audit.all,
    queryFn: async () => {
      const res = await get<{ items: BackendPolicyAuditEntry[] }>(`/policy/audit`);
      return { items: (res.items ?? []).map(mapPolicyAuditEntry) };
    },
    staleTime: 15_000,
  });

  // Memoize the filter object against each scalar/array field so two
  // identical-content filter instances reuse the same memo result without
  // the JSON.stringify allocation on every render. Arrays are joined into
  // stable scalar keys; deep-nested object filters are not used here.
  const stableFilters = useMemo<AuditFilters>(
    () => filters,
    [
      filters.eventType?.join("|") ?? "",
      filters.actor ?? "",
      filters.resourceType ?? "",
      filters.resourceId ?? "",
      filters.severity?.join("|") ?? "",
      filters.outcome?.join("|") ?? "",
      filters.timeRange ?? "",
      filters.from ?? "",
      filters.to ?? "",
      filters.search ?? "",
      filters.page ?? 0,
      filters.perPage ?? 0,
      filters.sort ?? "",
    ],
  );

  const filtered = useMemo(() => {
    if (!query.data?.items) return [];
    return applySort(applyFilters(query.data.items, stableFilters), stableFilters.sort);
  }, [query.data, stableFilters]);

  return { ...query, filtered };
}

export type ExportFormat = "csv" | "json";

export function useAuditEvent(eventId: string | null) {
  const queryClient = useQueryClient();
  return useQuery<AuditEntry | null>({
    queryKey: queryKeys.audit.event(eventId),
    queryFn: async () => {
      const res = await get<{ items: BackendPolicyAuditEntry[] }>("/policy/audit");
      const entries = (res.items ?? []).map(mapPolicyAuditEntry);
      return entries.find((e) => e.id === eventId) ?? null;
    },
    enabled: !!eventId,
    placeholderData: () => {
      const cached = queryClient.getQueryData<ApiResponse<AuditEntry[]>>(queryKeys.audit.all);
      return cached?.items?.find((e) => e.id === eventId);
    },
  });
}

export function useAuditCorrelation(resourceId: string | null) {
  const queryClient = useQueryClient();
  return useQuery<AuditEntry[]>({
    queryKey: queryKeys.audit.correlation(resourceId),
    queryFn: async () => {
      const res = await get<{ items: BackendPolicyAuditEntry[] }>("/policy/audit");
      const all = (res.items ?? []).map(mapPolicyAuditEntry);
      return all
        .filter((e) => e.resourceId === resourceId)
        .sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime());
    },
    enabled: !!resourceId,
    placeholderData: () => {
      const cached = queryClient.getQueryData<ApiResponse<AuditEntry[]>>(queryKeys.audit.all);
      if (!cached?.items) return undefined;
      return cached.items
        .filter((e) => e.resourceId === resourceId)
        .sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime());
    },
  });
}

export function useAuditExport(
  filters: AuditFilters,
  format: ExportFormat,
  enabled: boolean,
) {
  return useQuery<ApiResponse<AuditEntry[]>>({
    queryKey: queryKeys.audit.export(filters, format),
    queryFn: () => {
      return get<{ items: BackendPolicyAuditEntry[] }>("/policy/audit").then((res) => ({
        items: (res.items ?? []).map(mapPolicyAuditEntry),
      }));
    },
    enabled,
    staleTime: 0,
  });
}

/** @internal exported for unit tests */
export const __auditInternal = {
  applyFilters,
  applySort,
};
