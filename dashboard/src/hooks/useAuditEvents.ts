import { useMemo } from "react";
import {
  useInfiniteQuery,
  useQuery,
  type InfiniteData,
  type UseInfiniteQueryResult,
  type UseQueryResult,
} from "@tanstack/react-query";
import { get, ApiError } from "../api/client";
import type { AuditEntry } from "../api/types";
import {
  mapAuditEvent,
  type SiemAuditEventInput,
} from "../api/transform";

// AuditFilters mirrors useAudit.ts so consumers can swap in place.
// Fields that don't have a server-side equivalent (page/perPage/sort)
// are applied client-side after the SIEM page is fetched, matching
// the previous behavior with the policy-bundle audit feed.
export interface AuditEventsFilters {
  eventType?: string[];
  severity?: string[];
  from?: string;
  to?: string;
  search?: string;
  // category narrows to the server governance/routine taxonomy ("governance"
  // backs the Hide system/routine toggle). agentId is an exact agent-id match;
  // agent is a human agent-name substring (task-c8d4b056).
  category?: string;
  agentId?: string;
  agent?: string;
  cursor?: string;
  limit?: number;
}

// AUDIT_LIVE_INTERVAL_MS is the poll cadence used when the Audit Log "Live"
// toggle is ON (P2a / demo): new decisions stream in within ~2s. Live is OFF by
// default so normal users keep the calm staleTime:15_000 cadence + prod load.
export const AUDIT_LIVE_INTERVAL_MS = 2000;

// AuditLiveOptions opts a consumer into live polling. `live` is a fetch CADENCE,
// not a data dimension, so it is deliberately kept OUT of the queryKey (toggling
// it must not invalidate/refetch the cached pages, only change refetchInterval).
export interface AuditLiveOptions {
  live?: boolean;
}

// Wire-level envelope returned by GET /api/v1/audit/events.
interface AuditEventsEnvelope {
  items: SiemAuditEventInput[];
  next_cursor: string;
  returned: number;
}

interface UseAuditEventsResult
  extends Omit<UseQueryResult<AuditEventsEnvelope, ApiError>, "data"> {
  items: AuditEntry[];
  nextCursor: string;
  hasNextPage: boolean;
  // userMessage is a human-friendly summary surfaced for actionable
  // backend errors (503 audit_chainer_not_installed in particular). The
  // existing UseQueryResult.error has the raw ApiError; consumers that
  // just want something to render in an ErrorBanner read this field.
  userMessage?: string;
}

function buildQueryString(filters: AuditEventsFilters): string {
  const params = new URLSearchParams();
  if (filters.limit !== undefined && filters.limit > 0) {
    params.set("limit", String(filters.limit));
  }
  if (filters.cursor) params.set("cursor", filters.cursor);
  // Server side accepts a single event_type. If the caller asked for
  // multiple, we forward the first and apply the rest client-side; the
  // server is the authoritative narrowing on the wire.
  if (filters.eventType && filters.eventType.length > 0) {
    params.set("event_type", filters.eventType[0]);
  }
  if (filters.severity && filters.severity.length > 0) {
    params.set("severity", filters.severity[0]);
  }
  if (filters.from) params.set("from", filters.from);
  if (filters.to) params.set("to", filters.to);
  if (filters.search) params.set("search", filters.search);
  if (filters.category) params.set("category", filters.category);
  if (filters.agentId) params.set("agent_id", filters.agentId);
  if (filters.agent) params.set("agent", filters.agent);
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

// Map a 503 audit_chainer_not_installed (or generic non-2xx) into a
// human-readable message for the Audit Log error banner. The raw error
// goes into `error` for engineering; this is the customer-facing line.
function deriveUserMessage(err: unknown): string | undefined {
  if (!(err instanceof ApiError)) return undefined;
  if (err.status === 503) {
    return "Audit chain not installed — contact your operator. The Audit Log surface requires the gateway's audit chainer (CORDUM_AUDIT_HMAC_KEY) to be configured.";
  }
  if (err.status === 403) {
    return "You don't have permission to read the audit log.";
  }
  return `Failed to load audit log (${err.status}).`;
}

// useAuditEvents fetches the SIEM-feed audit page. Mirrors useAudit's
// query shape so the Audit Log page can swap call sites without
// rewriting state plumbing.
export function useAuditEvents(
  filters: AuditEventsFilters = {},
  options: AuditLiveOptions = {},
): UseAuditEventsResult {
  // Stabilise the filter object so React Query doesn't refetch on
  // identical-content rerenders. Same scalar-join strategy as useAudit.
  const stableFilters = useMemo<AuditEventsFilters>(
    () => filters,
    [
      filters.eventType?.join("|") ?? "",
      filters.severity?.join("|") ?? "",
      filters.from ?? "",
      filters.to ?? "",
      filters.search ?? "",
      filters.category ?? "",
      filters.agentId ?? "",
      filters.agent ?? "",
      filters.cursor ?? "",
      filters.limit ?? 0,
    ],
  );

  const query = useQuery<AuditEventsEnvelope, ApiError>({
    queryKey: ["audit-events", stableFilters],
    queryFn: async () => {
      return get<AuditEventsEnvelope>(
        `/audit/events${buildQueryString(stableFilters)}`,
      );
    },
    staleTime: 15_000,
    // Live poll opt-in (P2a): ~2s cadence when enabled, else no auto-refetch.
    refetchInterval: options.live ? AUDIT_LIVE_INTERVAL_MS : false,
  });

  const items = useMemo<AuditEntry[]>(() => {
    const raw = query.data?.items ?? [];
    return raw.map(mapAuditEvent);
  }, [query.data]);

  const nextCursor = query.data?.next_cursor ?? "";
  const userMessage = deriveUserMessage(query.error);

  const { data: _unused, ...rest } = query;
  return {
    ...rest,
    items,
    nextCursor,
    hasNextPage: nextCursor !== "",
    userMessage,
  };
}

// Infinite-query variant of useAuditEvents — paginates through the SIEM
// feed by chaining the server's `next_cursor` across pages, flattening
// the pages into a single AuditEntry[] for table rendering. Use this
// when the consumer needs to surface MORE than one page of events
// (default page = 100, hard cap = 200 server-side); the bare
// useAuditEvents above is the one-shot fetch suited for fixed-size
// surfaces (e.g. a dashboard widget that only ever shows the latest
// page).
//
// The shape mirrors UseInfiniteQueryResult fields most consumers need:
//   - items: AuditEntry[] flattened from all loaded pages
//   - hasNextPage: server emitted a non-empty next_cursor on the last page
//   - isFetchingNextPage: TanStack Query's in-flight indicator for the
//     subsequent page fetch
//   - fetchNextPage: call this to load the next page (typically wired to
//     a "Load more" button or an IntersectionObserver sentinel)
//   - userMessage: same 503/403 translation as useAuditEvents
interface UseInfiniteAuditEventsResult
  extends Omit<
    UseInfiniteQueryResult<InfiniteData<AuditEventsEnvelope>, ApiError>,
    "data"
  > {
  items: AuditEntry[];
  // The raw response pages — exposed so consumers that need server
  // metadata (e.g. the per-page `returned` count, or the most recent
  // next_cursor for debug-style UI) can read it without reconstructing.
  pages: AuditEventsEnvelope[];
  userMessage?: string;
}

export function useInfiniteAuditEvents(
  filters: AuditEventsFilters = {},
  options: AuditLiveOptions = {},
): UseInfiniteAuditEventsResult {
  // Stabilise the filter object across renders so a parent rerender
  // with new-object-same-content filters doesn't blow away the cached
  // pages. Drops `cursor` from the dep set because cursor pagination
  // is internal to this hook — the caller never passes it directly.
  const stableFilters = useMemo<Omit<AuditEventsFilters, "cursor">>(
    () => ({
      eventType: filters.eventType,
      severity: filters.severity,
      from: filters.from,
      to: filters.to,
      search: filters.search,
      category: filters.category,
      agentId: filters.agentId,
      agent: filters.agent,
      limit: filters.limit,
    }),
    [
      filters.eventType?.join("|") ?? "",
      filters.severity?.join("|") ?? "",
      filters.from ?? "",
      filters.to ?? "",
      filters.search ?? "",
      filters.category ?? "",
      filters.agentId ?? "",
      filters.agent ?? "",
      filters.limit ?? 0,
    ],
  );

  const query = useInfiniteQuery<
    AuditEventsEnvelope,
    ApiError,
    InfiniteData<AuditEventsEnvelope>,
    readonly unknown[],
    string
  >({
    queryKey: ["audit-events-infinite", stableFilters],
    initialPageParam: "",
    queryFn: async ({ pageParam }) => {
      // pageParam comes from the previous page's next_cursor (or "" on
      // the first request); forward it verbatim to the server so the
      // opaque Redis Stream ID stays opaque end-to-end.
      const params: AuditEventsFilters = { ...stableFilters };
      if (pageParam) {
        params.cursor = pageParam;
      }
      return get<AuditEventsEnvelope>(
        `/audit/events${buildQueryString(params)}`,
      );
    },
    getNextPageParam: (lastPage) =>
      lastPage.next_cursor ? lastPage.next_cursor : undefined,
    staleTime: 15_000,
    // Live poll opt-in (P2a): ~2s cadence when enabled, else no auto-refetch.
    refetchInterval: options.live ? AUDIT_LIVE_INTERVAL_MS : false,
  });

  const pages = useMemo<AuditEventsEnvelope[]>(
    () => query.data?.pages ?? [],
    [query.data],
  );

  const items = useMemo<AuditEntry[]>(() => {
    return pages.flatMap((page) => page.items.map(mapAuditEvent));
  }, [pages]);

  const userMessage = deriveUserMessage(query.error);

  const { data: _unused, ...rest } = query;
  return {
    ...rest,
    items,
    pages,
    userMessage,
  };
}
