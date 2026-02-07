import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import type { AuditEntry, ApiResponse } from "../api/types";
import { mapPolicyAuditEntry, type BackendPolicyAuditEntry } from "../api/transform";

// ---------------------------------------------------------------------------
// Filters
// ---------------------------------------------------------------------------

export interface AuditFilters {
  eventType?: string[];
  actor?: string;
  resourceType?: string;
  timeRange?: string;
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
  if (f.timeRange && TIME_RANGE_MS[f.timeRange]) {
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
        e.resourceId.toLowerCase().includes(lower),
    );
  }

  return result;
}

function applySort(entries: AuditEntry[], sort?: string): AuditEntry[] {
  if (!sort) return entries;
  const sorted = [...entries];
  switch (sort) {
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
    queryKey: ["audit"],
    queryFn: async () => {
      const res = await get<{ items: BackendPolicyAuditEntry[] }>(`/policy/audit`);
      return { items: (res.items ?? []).map(mapPolicyAuditEntry) };
    },
    staleTime: 15_000,
  });

  const filtered = useMemo(() => {
    if (!query.data?.items) return [];
    return applySort(applyFilters(query.data.items, filters), filters.sort);
  }, [query.data, filters]);

  return { ...query, filtered };
}

export type ExportFormat = "csv" | "json";

export function useAuditExport(
  filters: AuditFilters,
  format: ExportFormat,
  enabled: boolean,
) {
  return useQuery<ApiResponse<AuditEntry[]>>({
    queryKey: ["audit-export", filters, format],
    queryFn: () => {
      return get<{ items: BackendPolicyAuditEntry[] }>("/policy/audit").then((res) => ({
        items: (res.items ?? []).map(mapPolicyAuditEntry),
      }));
    },
    enabled,
    staleTime: 0,
  });
}
