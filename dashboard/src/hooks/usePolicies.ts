import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post, put, del } from "../api/client";
import { logger } from "../lib/logger";
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

// Feature flags (disabled by default until gateway endpoints are available).
export const POLICY_CONFIG_SUPPORTED =
  import.meta.env.VITE_POLICY_CONFIG_SUPPORTED === "true";
export const POLICY_STATS_SUPPORTED =
  import.meta.env.VITE_POLICY_STATS_SUPPORTED === "true";

export function encodePolicyBundleId(id: string): string {
  return id.replaceAll("/", "~");
}

function policyBundlePath(id: string): string {
  return `/policy/bundles/${encodePolicyBundleId(id)}`;
}

function policyBundleRulePath(bundleId: string, ruleId: string): string {
  return `${policyBundlePath(bundleId)}/rules/${encodeURIComponent(ruleId)}`;
}

function policyBundleSimulatePath(bundleId: string): string {
  return `${policyBundlePath(bundleId)}/simulate`;
}

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
      const res = await get<BackendPolicyBundleDetail>(policyBundlePath(id));
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
    mutationFn: ({ bundleId, note, message, author }) => {
      logger.info("policies", "Publishing policy", { bundleId });
      return post<void>("/policy/publish", { bundle_ids: [bundleId], note, message, author });
    },
    onSuccess: (_, { bundleId }) => {
      logger.info("policies", "Policy published", { bundleId });
      queryClient.invalidateQueries({ queryKey: ["policy-bundles"] });
      queryClient.invalidateQueries({ queryKey: ["policy-snapshots"] });
    },
    onError: (err, { bundleId }) => {
      logger.error("policies", "Publish failed", { bundleId, error: err.message });
    },
  });
}

export function useRollbackPolicy() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { snapshotId: string; note?: string; message?: string; author?: string }>({
    mutationFn: ({ snapshotId, note, message, author }) => {
      logger.info("policies", "Rolling back policy", { snapshotId });
      return post<void>("/policy/rollback", { snapshot_id: snapshotId, note, message, author });
    },
    onSuccess: (_, { snapshotId }) => {
      logger.info("policies", "Policy rolled back", { snapshotId });
      queryClient.invalidateQueries({ queryKey: ["policy-bundles"] });
      queryClient.invalidateQueries({ queryKey: ["policy-snapshots"] });
      queryClient.invalidateQueries({ queryKey: ["policy-rules"] });
    },
    onError: (err, { snapshotId }) => {
      logger.error("policies", "Rollback failed", { snapshotId, error: err.message });
    },
  });
}

export function useToggleRule() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { bundleId: string; ruleId: string; enabled: boolean }>({
    mutationFn: ({ bundleId, ruleId, enabled }) => {
      logger.info("policies", "Toggling rule", { bundleId, ruleId, enabled });
      return put<void>(policyBundleRulePath(bundleId, ruleId), { enabled });
    },
    onSuccess: (_, { bundleId, ruleId, enabled }) => {
      logger.info("policies", "Rule toggled", { bundleId, ruleId, enabled });
      queryClient.invalidateQueries({ queryKey: ["policy-bundle", bundleId] });
      queryClient.invalidateQueries({ queryKey: ["policy-bundles"] });
      queryClient.invalidateQueries({ queryKey: ["policy-rules"] });
    },
    onError: (err, { bundleId, ruleId }) => {
      logger.error("policies", "Toggle rule failed", { bundleId, ruleId, error: err.message });
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
        policyBundleSimulatePath(input.bundleId),
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

// ---------------------------------------------------------------------------
// Policy config — default stance + lockdown
// ---------------------------------------------------------------------------

export interface PolicyConfig {
  defaultStance: "allow" | "deny";
  lockdown: boolean;
  lockdownReason?: string;
  lockdownBy?: string;
  lockdownAt?: string;
}

const DEFAULT_POLICY_CONFIG: PolicyConfig = { defaultStance: "allow", lockdown: false };

export function usePolicyConfig() {
  return useQuery<PolicyConfig>({
    queryKey: ["policy-config"],
    queryFn: async () => {
      if (!POLICY_CONFIG_SUPPORTED) return DEFAULT_POLICY_CONFIG;
      return get<PolicyConfig>("/policy/config");
    },
    enabled: POLICY_CONFIG_SUPPORTED,
    initialData: DEFAULT_POLICY_CONFIG,
    staleTime: 10_000,
  });
}

export function useUpdatePolicyConfig() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, Partial<PolicyConfig>>({
    mutationFn: (config) => {
      logger.info("policies", "Updating policy config", { config });
      if (!POLICY_CONFIG_SUPPORTED) {
        return Promise.resolve();
      }
      return put<void>("/policy/config", config);
    },
    onSuccess: () => {
      logger.info("policies", "Policy config updated");
      queryClient.invalidateQueries({ queryKey: ["policy-config"] });
    },
    onError: (err) => {
      logger.error("policies", "Policy config update failed", { error: err.message });
    },
  });
}

export function useActivateLockdown() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { reason: string }>({
    mutationFn: ({ reason }) => {
      logger.warn("policies", "Activating lockdown", { reason });
      if (!POLICY_CONFIG_SUPPORTED) {
        return Promise.resolve();
      }
      return post<void>("/policy/lockdown", { reason });
    },
    onSuccess: () => {
      logger.warn("policies", "Lockdown activated");
      queryClient.invalidateQueries({ queryKey: ["policy-config"] });
    },
    onError: (err) => {
      logger.error("policies", "Lockdown activation failed", { error: err.message });
    },
  });
}

export function useDeactivateLockdown() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, void>({
    mutationFn: () => {
      logger.info("policies", "Deactivating lockdown");
      if (!POLICY_CONFIG_SUPPORTED) {
        return Promise.resolve();
      }
      return del<void>("/policy/lockdown");
    },
    onSuccess: () => {
      logger.info("policies", "Lockdown deactivated");
      queryClient.invalidateQueries({ queryKey: ["policy-config"] });
    },
    onError: (err) => {
      logger.error("policies", "Lockdown deactivation failed", { error: err.message });
    },
  });
}

// ---------------------------------------------------------------------------
// Policy approvals — frontend-derived pending queue
// ---------------------------------------------------------------------------

export interface PendingPolicyChange {
  bundle: PolicyBundle;
  changeSummary: string;
}

/**
 * Derives a list of pending policy changes by comparing each bundle's
 * `updatedAt` vs `publishedAt`. A bundle is "pending review" if it has
 * rules and has been updated after its last publish (or never published).
 *
 * This is a frontend-computed approval queue until the backend adds a
 * dedicated policy approval endpoint.
 */
export function usePolicyApprovals() {
  const { data, isLoading, isError } = usePolicyBundles();
  const bundles = data?.items ?? [];

  const pending: PendingPolicyChange[] = [];
  for (const b of bundles) {
    const updatedMs = b.updatedAt ? new Date(b.updatedAt).getTime() : 0;
    const publishedMs = b.publishedAt ? new Date(b.publishedAt).getTime() : 0;

    if (b.rules.length > 0 && (!b.publishedAt || updatedMs > publishedMs + 1000)) {
      const changeSummary = !b.publishedAt
        ? `${b.rules.length} new rule${b.rules.length !== 1 ? "s" : ""}`
        : `${b.rules.length} rule${b.rules.length !== 1 ? "s" : ""} modified`;
      pending.push({ bundle: b, changeSummary });
    }
  }

  return { pending, isLoading, isError };
}
