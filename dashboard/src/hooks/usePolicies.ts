import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { get, post, put, del } from "../api/client";
import { logger } from "../lib/logger";
import { useToastStore } from "../state/toast";
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

function readPolicyBundleContent(raw: unknown): string {
  if (typeof raw === "string") return raw;
  if (typeof raw === "object" && raw !== null) {
    const obj = raw as Record<string, unknown>;
    if (typeof obj.content === "string" && obj.content) return obj.content;
    if (typeof obj.policy === "string" && obj.policy) return obj.policy;
    if (typeof obj.data === "string" && obj.data) return obj.data;
  }
  return "";
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
      const res = await get<{
        items: BackendPolicyBundleSummary[];
        bundles?: Record<string, { content?: string } | string>;
      }>("/policy/bundles");
      const bundlesMap = res.bundles ?? {};
      return {
        items: (res.items ?? []).map((summary) => {
          const content = readPolicyBundleContent(bundlesMap[summary.id]);
          return mapPolicyBundleSummary(summary, content);
        }),
      };
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
      useToastStore.getState().addToast({ type: "success", title: "Policy published" });
      queryClient.invalidateQueries({ queryKey: ["policy-bundle"] });
      queryClient.invalidateQueries({ queryKey: ["policy-bundles"] });
      queryClient.invalidateQueries({ queryKey: ["policy-rules"] });
      queryClient.invalidateQueries({ queryKey: ["policy-snapshots"] });
    },
    onError: (err, { bundleId }) => {
      logger.error("policies", "Publish failed", { bundleId, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to publish", description: err.message });
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
      useToastStore.getState().addToast({ type: "success", title: "Policy rolled back" });
      queryClient.invalidateQueries({ queryKey: ["policy-bundle"] });
      queryClient.invalidateQueries({ queryKey: ["policy-bundles"] });
      queryClient.invalidateQueries({ queryKey: ["policy-snapshots"] });
      queryClient.invalidateQueries({ queryKey: ["policy-rules"] });
    },
    onError: (err, { snapshotId }) => {
      logger.error("policies", "Rollback failed", { snapshotId, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Rollback failed", description: err.message });
    },
  });
}

export function useCaptureSnapshot() {
  const queryClient = useQueryClient();
  return useMutation<{ snapshot_id: string; captured_at: string }, Error, { name?: string; note?: string }>({
    mutationFn: (input) => {
      logger.info("policies", "Capturing snapshot", { name: input.name, note: input.note });
      return post<{ snapshot_id: string; captured_at: string }>("/policy/bundles/snapshots", input);
    },
    onSuccess: () => {
      logger.info("policies", "Snapshot captured");
      useToastStore.getState().addToast({ type: "success", title: "Snapshot captured" });
      queryClient.invalidateQueries({ queryKey: ["policy-snapshots"] });
    },
    onError: (err) => {
      logger.error("policies", "Snapshot capture failed", { error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Snapshot failed", description: err.message });
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
      useToastStore.getState().addToast({ type: "success", title: "Rule updated" });
      queryClient.invalidateQueries({ queryKey: ["policy-bundle", bundleId] });
      queryClient.invalidateQueries({ queryKey: ["policy-bundles"] });
      queryClient.invalidateQueries({ queryKey: ["policy-rules"] });
    },
    onError: (err, { bundleId, ruleId }) => {
      logger.error("policies", "Toggle rule failed", { bundleId, ruleId, error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to update rule", description: err.message });
    },
  });
}

export interface UpdatePolicyBundleInput {
  id: string;
  content: string;
  author?: string;
  message?: string;
  enabled?: boolean;
}

export interface UpdatePolicyBundleResponse {
  id: string;
  updated_at?: string;
}

export function useUpdatePolicyBundle() {
  const queryClient = useQueryClient();
  return useMutation<UpdatePolicyBundleResponse, Error, UpdatePolicyBundleInput>({
    mutationFn: async ({ id, content, author, message, enabled }) => {
      const normalizedID = id.trim();
      const normalizedContent = content.trim();
      if (!normalizedID) {
        throw new Error("bundle id required");
      }
      if (!normalizedContent) {
        throw new Error("content required");
      }

      const payload: Record<string, unknown> = { content: normalizedContent };
      if (author && author.trim()) payload.author = author.trim();
      if (message && message.trim()) payload.message = message.trim();
      if (typeof enabled === "boolean") payload.enabled = enabled;

      logger.info("policies", "Updating policy bundle", { bundleId: normalizedID });
      return put<UpdatePolicyBundleResponse>(policyBundlePath(normalizedID), payload);
    },
    onSuccess: (_, { id }) => {
      const normalizedID = id.trim();
      logger.info("policies", "Policy bundle updated", { bundleId: normalizedID });
      useToastStore
        .getState()
        .addToast({ type: "success", title: "Policy bundle updated" });
      queryClient.invalidateQueries({ queryKey: ["policy-bundles"] });
      queryClient.invalidateQueries({ queryKey: ["policy-bundle", normalizedID] });
      queryClient.invalidateQueries({ queryKey: ["policy-rules"] });
    },
    onError: (err, { id }) => {
      logger.error("policies", "Update policy bundle failed", {
        bundleId: id.trim(),
        error: err.message,
      });
      useToastStore.getState().addToast({
        type: "error",
        title: "Failed to update bundle",
        description: err.message,
      });
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
  resourceName?: string;
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
        resourceName: entry.resource_name || undefined,
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
// Mutation — explain
// ---------------------------------------------------------------------------

export interface ExplainCondition {
  field: string;
  operator: string;
  expected: string;
  actual: string;
  passed: boolean;
}

export interface ExplainRuleStep {
  ruleId: string;
  ruleName?: string;
  decision: string;
  reason: string;
  matched: boolean;
  conditions: ExplainCondition[];
}

export interface ExplainResult {
  decision: string;
  matchedRule?: string;
  reason?: string;
  evaluationTimeMs?: number;
  policySnapshot?: string;
  evaluationChain: ExplainRuleStep[];
  raw: Record<string, unknown>;
}

function mapExplainCondition(raw: Record<string, unknown>): ExplainCondition {
  return {
    field: String(raw.field ?? raw.key ?? ""),
    operator: String(raw.operator ?? raw.op ?? "match"),
    expected: String(raw.expected ?? raw.want ?? ""),
    actual: String(raw.actual ?? raw.got ?? ""),
    passed: Boolean(raw.passed ?? raw.ok ?? false),
  };
}

function mapExplainRuleStep(raw: Record<string, unknown>): ExplainRuleStep {
  const conditions = Array.isArray(raw.conditions)
    ? (raw.conditions as Record<string, unknown>[]).map(mapExplainCondition)
    : [];
  return {
    ruleId: String(raw.rule_id ?? raw.ruleId ?? "unknown"),
    ruleName: typeof raw.rule_name === "string" ? raw.rule_name : undefined,
    decision: String(raw.decision ?? ""),
    reason: String(raw.reason ?? ""),
    matched: Boolean(raw.matched),
    conditions,
  };
}

export interface ExplainInput {
  request: Record<string, unknown>;
}

export function useExplainPolicy() {
  return useMutation<ExplainResult, Error, ExplainInput>({
    mutationFn: async (input) => {
      const res = await post<Record<string, unknown>>("/policy/explain", {
        ...input.request,
      });
      const rawDecision =
        typeof res.decision === "string"
          ? res.decision
          : typeof res.decisionType === "string"
            ? res.decisionType
            : "";
      const decision = normalizeDecisionType(rawDecision);

      const evalPath = Array.isArray(res.evaluation_path)
        ? (res.evaluation_path as Record<string, unknown>[]).map(mapExplainRuleStep)
        : [];

      return {
        decision,
        matchedRule: String(res.rule_id ?? res.matched_rule_id ?? res.matchedRule ?? ""),
        reason: typeof res.reason === "string" ? res.reason : undefined,
        evaluationTimeMs: Number(res.eval_time_ms ?? res.evalTimeMs ?? 0) || undefined,
        policySnapshot: typeof res.policy_snapshot === "string" ? res.policy_snapshot : undefined,
        evaluationChain: evalPath,
        raw: res,
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
      useToastStore.getState().addToast({ type: "success", title: "Policy config saved" });
      queryClient.invalidateQueries({ queryKey: ["policy-config"] });
    },
    onError: (err) => {
      logger.error("policies", "Policy config update failed", { error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to save config", description: err.message });
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
      useToastStore.getState().addToast({ type: "warning", title: "Lockdown activated" });
      queryClient.invalidateQueries({ queryKey: ["policy-config"] });
    },
    onError: (err) => {
      logger.error("policies", "Lockdown activation failed", { error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to activate lockdown", description: err.message });
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
      useToastStore.getState().addToast({ type: "success", title: "Lockdown deactivated" });
      queryClient.invalidateQueries({ queryKey: ["policy-config"] });
    },
    onError: (err) => {
      logger.error("policies", "Lockdown deactivation failed", { error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Failed to deactivate lockdown", description: err.message });
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

/** @internal exported for unit tests */
export const __policiesInternal = {
  readPolicyBundleContent,
  policyBundlePath,
  policyBundleRulePath,
  policyBundleSimulatePath,
  DEFAULT_POLICY_CONFIG,
};
