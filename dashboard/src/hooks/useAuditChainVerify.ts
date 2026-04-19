import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";

// AuditVerifyGap mirrors the Go type in core/audit/chain_verify.go.
// Kept as a plain interface so the dashboard does not depend on a
// generated-type pipeline — the JSON shape is small and stable.
export interface AuditVerifyGap {
  at_seq: number;
  type:
    | "missing"
    | "out_of_order"
    | "hash_mismatch"
    | "retention_trimmed";
}

export interface AuditVerifyResult {
  status: "ok" | "compromised" | "partial";
  total_events: number;
  verified_events: number;
  gaps: AuditVerifyGap[];
  retention_boundary_seq: number;
  retention_window_hours?: number;
  first_seq?: number;
  last_seq?: number;
}

export interface UseAuditChainVerifyOptions {
  /**
   * When false, the query is parked — no fetch, no poll. Callers that
   * surface the chain state to non-admins (who get 403 from the
   * admin-only /audit/verify endpoint) MUST pass `enabled: false`.
   *
   * Default true preserves back-compat with `AuditChainCard` on the
   * Command Center, which is always mounted for admin users.
   */
  enabled?: boolean;
}

// useAuditChainVerify polls GET /api/v1/audit/verify for the current
// tenant. The 5-minute interval matches the plan's cadence — fast
// enough to surface fresh incidents on the Command Center, slow
// enough that the walk (up to 10k events) is not hammering the
// gateway.
//
// Background refetching is disabled (refetchOnWindowFocus=false) so
// that a user cycling tabs does not produce a burst of verify
// requests that would show up in the audit log itself as
// admin-triggered activity.
//
// When opts.enabled is false, the query stays parked but still reads
// from cache — so a previously-fetched result is rendered without a
// re-fetch (matches React Query semantics for disabled queries).
export function useAuditChainVerify(
  tenant: string,
  opts?: UseAuditChainVerifyOptions,
) {
  const enabled = opts?.enabled ?? true;
  return useQuery({
    queryKey: ["audit-chain-verify", tenant] as const,
    queryFn: async () => {
      const q = new URLSearchParams();
      if (tenant) q.set("tenant", tenant);
      const suffix = q.toString() ? `?${q.toString()}` : "";
      return get<AuditVerifyResult>(`/audit/verify${suffix}`);
    },
    enabled,
    refetchInterval: enabled ? 5 * 60 * 1000 : false,
    refetchOnWindowFocus: false,
    // Stale-while-revalidate: show prior result while next poll runs.
    staleTime: 4 * 60 * 1000,
  });
}
