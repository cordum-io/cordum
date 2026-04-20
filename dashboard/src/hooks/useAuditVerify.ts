import { useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import {
  useAuditChainVerify,
  type AuditVerifyGap,
  type AuditVerifyResult,
} from "./useAuditChainVerify";
import { useVerificationStore } from "../state/verification";
import { useIsAdmin } from "./usePermission";

// Re-export canonical names required by the verification-dashboard plan.
// The underlying hook + types are shared with the AuditChainCard on the
// Command Center, but the dashboard calls the widget-facing API
// "useAuditVerify" / "ChainVerify…" so this layer keeps naming stable
// against the plan and the backend spec.
export type ChainVerifyStatus = AuditVerifyResult["status"];
export type ChainVerifyGapType = AuditVerifyGap["type"];
export type ChainVerifyGap = AuditVerifyGap;
export type ChainVerifyResult = AuditVerifyResult;

export interface UseAuditVerifyOptions {
  tenant: string;
  /**
   * Explicit opt-in for network fetch. Defaults to `false` — the
   * widget-facing wrapper is MANUAL by default so page-mount on the
   * Policy Overview / Governance Verification surfaces never
   * auto-triggers an admin-only API call for non-admin viewers.
   *
   * Set `enabled: true` from the widget after the user clicks the
   * "Run chain check" / "Re-verify" button. Once enabled, React
   * Query keeps the cached result visible even if `enabled` flips
   * back to false on a subsequent render — that's how the widget
   * restores state after a remount without re-fetching.
   */
  enabled?: boolean;
}

/**
 * Subscribe to the tenant's audit-chain verification status.
 *
 * Two gates apply on top of the caller's `enabled` flag:
 *
 *   1. Admin role — the backend `/audit/verify` handler is admin-only
 *      (`handlers_audit_verify.go` → `requireStoreAndRole(admin)`).
 *      Non-admin viewers never fire the request; the widget falls
 *      back to a read-only "requires admin" view.
 *   2. Explicit opt-in — the widget must flip `enabled: true` after
 *      the user asks for a check. This keeps the rollout honest to
 *      the DoD's "one-click verify" contract.
 *
 * Cached results (from a prior admin-triggered verify in the same
 * React Query session) continue to render even with `enabled: false`.
 */
export function useAuditVerify(opts: UseAuditVerifyOptions) {
  const isAdmin = useIsAdmin();
  const requestedEnabled = opts.enabled ?? false;
  const shouldFetch = requestedEnabled && isAdmin;
  const query = useAuditChainVerify(opts.tenant, { enabled: shouldFetch });

  const recordLastVerified = useVerificationStore((s) => s.setLastVerifiedAt);
  const dataUpdatedAt = query.dataUpdatedAt;
  // Push the timestamp into the persisted Zustand slice whenever
  // React Query receives fresh data. We key by tenant so an admin
  // managing multiple tenants sees the correct "last verified at"
  // for the one they're viewing. Guarded by `dataUpdatedAt > 0`
  // because a disabled query returns 0 here and we mustn't record
  // a fake "Just now" timestamp from that path.
  if (dataUpdatedAt > 0 && opts.tenant) {
    const existing = useVerificationStore.getState().lastVerifiedAt[opts.tenant];
    if (existing !== dataUpdatedAt) {
      recordLastVerified(opts.tenant, dataUpdatedAt);
    }
  }

  return query;
}

/**
 * Imperatively re-verify the chain — invalidates React Query's cache
 * entry for the current tenant's verify call so the next enabled
 * render kicks off a fresh request. Returns a stable callback so the
 * "Re-verify" button preserves its onClick identity across renders.
 *
 * NOTE: invalidation alone does NOT fetch when the query is disabled.
 * Callers must also flip the local `enabled` flag (typically via
 * `setSessionTriggered(true)` in the widget) so the query switches
 * into active mode.
 */
export function useTriggerAuditVerify(tenant: string) {
  const qc = useQueryClient();
  return useCallback(() => {
    qc.invalidateQueries({ queryKey: ["audit-chain-verify", tenant] });
  }, [qc, tenant]);
}

export type { AuditVerifyResult, AuditVerifyGap } from "./useAuditChainVerify";
