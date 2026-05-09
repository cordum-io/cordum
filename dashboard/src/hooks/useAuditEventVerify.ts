import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import type { AuditVerifyResult } from "./useAuditChainVerify";
import { useIsAdmin } from "./usePermission";

export interface UseAuditEventVerifyOptions {
  tenant: string;
  eventId: string;
  enabled?: boolean;
}

// useAuditEventVerify fires GET /audit/verify scoped to a single event
// (the drilldown's "single-event request, not bulk" contract from the
// task-55f813b3 DoD #4 amendment in comment-832277b0). Each row's
// expansion calls this with its own event ID, so every drilldown gets
// its own React Query cache entry and its own /audit/verify request —
// no shared chain-wide bulk read.
//
// The current backend handler walks the chain and returns AuditVerifyResult.
// `event_id` is forward-compatible: it labels each row's request at the URL
// level (so audit logs and proxy traces show which event the verify was
// for), and the dashboard derives the per-event verdict from the response
// using `event.seq` against `gaps` and `retention_boundary_seq`.
//
// Admin-gated: non-admin viewers never fire the request because
// `/audit/verify` is admin-only on the gateway.
export function useAuditEventVerify(opts: UseAuditEventVerifyOptions) {
  const isAdmin = useIsAdmin();
  const requestedEnabled = opts.enabled ?? false;
  const shouldFetch =
    requestedEnabled && isAdmin && !!opts.tenant && !!opts.eventId;
  return useQuery({
    queryKey: ["audit-event-verify", opts.tenant, opts.eventId] as const,
    queryFn: async () => {
      const q = new URLSearchParams();
      if (opts.tenant) q.set("tenant", opts.tenant);
      if (opts.eventId) q.set("event_id", opts.eventId);
      return get<AuditVerifyResult>(`/audit/verify?${q.toString()}`);
    },
    enabled: shouldFetch,
    refetchOnWindowFocus: false,
    staleTime: 4 * 60 * 1000,
  });
}
