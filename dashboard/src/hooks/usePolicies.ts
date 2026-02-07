import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post, put } from "../api/client";
import type {
  PolicyBundle,
  PolicyRule,
  ApiResponse,
  PolicySnapshotSummary,
  PolicySnapshot,
} from "../api/types";

export type { PolicySnapshot, PolicySnapshotSummary };
import {
  mapPolicyBundleSummary,
  mapPolicyBundleDetail,
  mapPolicyRule,
  mapPolicySnapshotSummary,
  mapPolicySnapshot,
  normalizeDecisionType,
  type BackendPolicyBundleSummary,
  type BackendPolicyBundleDetail,
  type BackendPolicySnapshotSummary,
  type BackendPolicySnapshot,
  type BackendPolicyAuditEntry,
} from "../api/transform";

// ---------------------------------------------------------------------------
// Queries — bundles
// ---------------------------------------------------------------------------

export function usePolicyBundles() {
  return useQuery<ApiResponse<PolicyBundle[]>>({
    queryKey: ["policy-bundles"],
    queryFn: async () => {
      const res = await get<{ items: BackendPolicyBundleSummary[] }>(
        "/policy/bundles",
      );
      return { items: (res.items ?? []).map(mapPolicyBundleSummary) };
    },
    staleTime: 30_000,
  });
}

export function usePolicyBundle(id: string) {
  return useQuery<PolicyBundle>({
    queryKey: ["policy-bundle", id],
    queryFn: async () => {
      const res = await get<BackendPolicyBundleDetail>(`/policy/bundles/${id}`);
      return mapPolicyBundleDetail(res);
    },
    enabled: !!id,
    staleTime: 30_000,
  });
}

// ---------------------------------------------------------------------------
// Queries — rules
// ---------------------------------------------------------------------------

export function usePolicyRules() {
  return useQuery<ApiResponse<PolicyRule[]>>({
    queryKey: ["policy-rules"],
    queryFn: async () => {
      const res = await get<{ items: Record<string, unknown>[] }>(
        "/policy/rules",
      );
      return { items: (res.items ?? []).map(mapPolicyRule) };
    },
    staleTime: 30_000,
  });
}

// ---------------------------------------------------------------------------
// Mutations — rules CRUD
// ---------------------------------------------------------------------------

// Rule CRUD endpoints are not available via the gateway API. Keep bundles
// editable via YAML instead.

// ---------------------------------------------------------------------------
// Mutations — publish / rollback
// ---------------------------------------------------------------------------

export function usePublishPolicy() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { bundleId: string; note?: string; message?: string; author?: string }>({
    mutationFn: ({ bundleId, note, message, author }) =>
      post<void>("/policy/publish", { bundle_ids: [bundleId], note, message, author }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["policy-bundles"] });
      queryClient.invalidateQueries({ queryKey: ["policy-snapshots"] });
    },
  });
}

export function useRollbackPolicy() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { snapshotId: string; note?: string; message?: string; author?: string }>({
    mutationFn: ({ snapshotId, note, message, author }) =>
      post<void>("/policy/rollback", { snapshot_id: snapshotId, note, message, author }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["policy-bundles"] });
      queryClient.invalidateQueries({ queryKey: ["policy-snapshots"] });
      queryClient.invalidateQueries({ queryKey: ["policy-rules"] });
    },
  });
}

// ---------------------------------------------------------------------------
// Queries — audit & snapshots
// ---------------------------------------------------------------------------

export interface PolicyAuditEntry {
  id: string;
  action: string;
  bundleId: string;
  actor: string;
  timestamp: string;
  details?: Record<string, unknown>;
}

export function usePolicyAudit() {
  return useQuery<ApiResponse<PolicyAuditEntry[]>>({
    queryKey: ["policy-audit"],
    queryFn: async () => {
      const res = await get<{ items: BackendPolicyAuditEntry[] }>("/policy/audit");
      const items = (res.items ?? []).map((entry) => ({
        id: entry.id,
        action: entry.action ?? "",
        bundleId: entry.resource_id ?? "",
        actor: entry.actor_id ?? entry.role ?? "",
        timestamp: entry.created_at ?? "",
        details: {
          bundle_ids: entry.bundle_ids,
          message: entry.message,
          snapshot_before: entry.snapshot_before,
          snapshot_after: entry.snapshot_after,
          resource_type: entry.resource_type,
        },
      }));
      return { items };
    },
    staleTime: 30_000,
  });
}

export function usePolicySnapshots() {
  return useQuery<ApiResponse<PolicySnapshotSummary[]>>({
    queryKey: ["policy-snapshots"],
    queryFn: async () => {
      const res = await get<{ items: BackendPolicySnapshotSummary[] }>("/policy/bundles/snapshots");
      return { items: (res.items ?? []).map(mapPolicySnapshotSummary) };
    },
    staleTime: 30_000,
  });
}

export function usePolicySnapshot(id: string | null) {
  return useQuery<PolicySnapshot>({
    queryKey: ["policy-snapshot", id],
    queryFn: async () => {
      const res = await get<BackendPolicySnapshot>(`/policy/bundles/snapshots/${id}`);
      return mapPolicySnapshot(res);
    },
    enabled: !!id,
    staleTime: 60_000,
  });
}

// ---------------------------------------------------------------------------
// Mutation — simulate
// ---------------------------------------------------------------------------

export interface SimulateInput {
  bundleId: string;
  request: Record<string, unknown>;
  content?: string;
}

export interface SimulateResult {
  decision: string;
  matchedRule?: string;
  reason?: string;
  evaluationTimeMs?: number;
  details: Record<string, unknown>;
}

export function useSimulatePolicy() {
  return useMutation<SimulateResult, Error, SimulateInput>({
    mutationFn: async (input) => {
      const res = await post<Record<string, unknown>>(
        `/policy/bundles/${input.bundleId}/simulate`,
        { request: input.request, content: input.content },
      );
      const rawDecision =
        typeof res.decision === "string"
          ? res.decision
          : typeof res.decisionType === "string"
            ? res.decisionType
            : "";
      const decision = normalizeDecisionType(rawDecision);
      return {
        decision,
        matchedRule: String(res.rule_id ?? res.matched_rule_id ?? res.matchedRule ?? ""),
        reason: typeof res.reason === "string" ? res.reason : undefined,
        evaluationTimeMs: Number(res.eval_time_ms ?? res.evalTimeMs ?? 0) || undefined,
        details: res,
      };
    },
  });
}
