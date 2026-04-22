import { useMemo } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError, get, post, put, del } from "../api/client";
import { logger } from "../lib/logger";
import { queryKeys } from "../lib/queryKeys";
import { useToastStore } from "../state/toast";
import type {
  PolicyBundle,
  PolicyRule,
  ApiResponse,
  PolicySnapshotSummary,
  PolicySnapshot,
  SafetyDecisionType,
  PolicyReplayRequest,
  PolicyReplayResponse,
  PolicyAnalyticsRequest,
  PolicyAnalyticsResponse,
} from "../api/types";

export type { PolicySnapshot, PolicySnapshotSummary };
import {
  mapPolicyBundleSummary,
  mapPolicyBundleDetail,
  mapPolicyRule,
  mapPolicySnapshotSummary,
  mapPolicySnapshot,
  normalizeDecisionType,
  readPolicyBundleSignature,
  type BackendPolicyBundleSummary,
  type BackendPolicyBundleDetail,
  type BackendPolicySnapshotSummary,
  type BackendPolicySnapshot,
  type BackendPolicyAuditEntry,
} from "../api/transform";

// Feature flags.
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

function readApiErrorDetail(error: ApiError): string | undefined {
  if (!error.body || typeof error.body !== "object") return undefined;
  const payload = error.body as Record<string, unknown>;
  const detail = [payload.error, payload.message, payload.details]
    .map((value) => (typeof value === "string" ? value.trim() : ""))
    .find(Boolean);
  return detail || undefined;
}

function describeBundleUpdateError(error: Error): string {
  if (error instanceof ApiError) {
    const detail = readApiErrorDetail(error);
    if (error.status === 409 || error.status === 412) {
      return detail
        ? `Policy bundle changed on server (${detail}). Refresh and retry.`
        : "Policy bundle changed on server. Refresh and retry.";
    }
    if (error.status === 400 || error.status === 422) {
      return detail
        ? `Policy bundle validation failed: ${detail}`
        : "Policy bundle validation failed. Check YAML and retry.";
    }
    if (detail) {
      return `Policy bundle request failed: ${detail}`;
    }
    return `Policy bundle request failed (status ${error.status}).`;
  }
  return error.message;
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
    queryKey: queryKeys.policies.bundles(),
    queryFn: async () => {
      const res = await get<{
        items: BackendPolicyBundleSummary[];
        bundles?: Record<string, unknown>;
      }>("/policy/bundles");
      const bundlesMap = res.bundles ?? {};
      return {
        items: (res.items ?? []).map((summary) => {
          const raw = bundlesMap[summary.id];
          const content = readPolicyBundleContent(raw);
          const signature = readPolicyBundleSignature(raw);
          return {
            ...mapPolicyBundleSummary(summary, content),
            content: content || undefined,
            signed: signature ? true : raw && typeof raw === "object" ? false : undefined,
            signature,
          };
        }),
      };
    },
    staleTime: 30_000,
  });
}

export function usePolicyBundle(id: string) {
  const queryClient = useQueryClient();
  return useQuery<PolicyBundle>({
    queryKey: queryKeys.policies.bundle(id),
    queryFn: async () => {
      const res = await get<BackendPolicyBundleDetail>(policyBundlePath(id));
      const detail = mapPolicyBundleDetail(res);
      // The detail endpoint does not yet surface the `_signature` map
      // attached by handlers_policy_bundles_signing.go. As a pragmatic
      // workaround, enrich the detail record from the bundles list
      // cache if it has been populated recently. This keeps the
      // Signature section on BundleDetailPage accurate when the user
      // navigates in from BundlesPage (the common path). When no
      // cached list entry exists the UI falls back to the 'unknown'
      // SignatureBadge state.
      const cachedList = queryClient.getQueryData<ApiResponse<PolicyBundle[]>>(
        queryKeys.policies.bundles(),
      );
      const cachedEntry = cachedList?.items?.find((b) => b.id === id);
      if (cachedEntry) {
        if (detail.signed === undefined) {
          detail.signed = cachedEntry.signed;
        }
        if (!detail.signature && cachedEntry.signature) {
          detail.signature = cachedEntry.signature;
        }
      }
      return detail;
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
    queryKey: queryKeys.policies.rules(),
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
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.bundle(bundleId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.bundles() });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.rules() });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.snapshots() });
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
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.bundles() });
      queryClient.invalidateQueries({ queryKey: ["policy-bundle"] });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.snapshots() });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.rules() });
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
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.snapshots() });
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
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.bundle(bundleId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.bundles() });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.rules() });
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
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.bundles() });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.bundle(normalizedID) });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.rules() });
    },
    onError: (err, { id }) => {
      const detail = describeBundleUpdateError(err);
      logger.error("policies", "Update policy bundle failed", {
        bundleId: id.trim(),
        error: detail,
      });
      useToastStore.getState().addToast({
        type: "error",
        title: "Failed to update bundle",
        description: detail,
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
    queryKey: queryKeys.policies.audit(),
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

export interface VelocityRuleMatch {
  topics: string[];
  tenants: string[];
  risk_tags: string[];
}

export interface VelocityRule {
  id: string;
  name: string;
  match: VelocityRuleMatch;
  window: string;
  key: string;
  threshold: number;
  decision: SafetyDecisionType;
  reason: string;
  enabled: boolean;
  createdAt?: string;
  updatedAt?: string;
}

export interface VelocityRuleStats {
  id: string;
  hitCount24h: number;
  hitRate24h: number;
  currentWindowCount: number;
  currentWindowMax: number;
  activeBuckets: number;
  exceededBuckets: number;
  lastTriggered?: string;
  hourlyHits: number[];
}

export interface VelocityRuleInput {
  id: string;
  name: string;
  match: Partial<VelocityRuleMatch>;
  window: string;
  key: string;
  threshold: number;
  decision: SafetyDecisionType;
  reason: string;
  enabled?: boolean;
}

export interface VelocityRuleListResult {
  items: VelocityRule[];
  count: number;
  limit: number;
  upgradeUrl?: string;
}

export interface VelocityRuleStatsResult {
  items: VelocityRuleStats[];
  topRules: VelocityRuleStats[];
  generatedAt?: string;
}

type BackendVelocityRule = {
  id?: string;
  name?: string;
  match?: {
    topics?: string[];
    tenants?: string[];
    risk_tags?: string[];
  };
  window?: string;
  key?: string;
  threshold?: number;
  decision?: string;
  reason?: string;
  enabled?: boolean;
  created_at?: string;
  updated_at?: string;
};

type BackendVelocityRuleStats = {
  id?: string;
  hit_count_24h?: number;
  hit_rate_24h?: number;
  current_window_count?: number;
  current_window_max?: number;
  active_buckets?: number;
  exceeded_buckets?: number;
  last_triggered?: string;
  hourly_hits?: number[];
};

function mapVelocityRule(raw: BackendVelocityRule): VelocityRule {
  return {
    id: raw.id?.trim() ?? "",
    name: raw.name?.trim() || raw.id?.trim() || "",
    match: {
      topics: raw.match?.topics ?? [],
      tenants: raw.match?.tenants ?? [],
      risk_tags: raw.match?.risk_tags ?? [],
    },
    window: raw.window?.trim() ?? "",
    key: raw.key?.trim() ?? "",
    threshold: typeof raw.threshold === "number" ? raw.threshold : 0,
    decision: normalizeDecisionType(raw.decision ?? "") as SafetyDecisionType,
    reason: raw.reason?.trim() ?? "",
    enabled: raw.enabled !== false,
    createdAt: raw.created_at?.trim() || undefined,
    updatedAt: raw.updated_at?.trim() || undefined,
  };
}

function mapVelocityRuleStats(raw: BackendVelocityRuleStats): VelocityRuleStats {
  return {
    id: raw.id?.trim() ?? "",
    hitCount24h: typeof raw.hit_count_24h === "number" ? raw.hit_count_24h : 0,
    hitRate24h: typeof raw.hit_rate_24h === "number" ? raw.hit_rate_24h : 0,
    currentWindowCount:
      typeof raw.current_window_count === "number" ? raw.current_window_count : 0,
    currentWindowMax:
      typeof raw.current_window_max === "number" ? raw.current_window_max : 0,
    activeBuckets: typeof raw.active_buckets === "number" ? raw.active_buckets : 0,
    exceededBuckets:
      typeof raw.exceeded_buckets === "number" ? raw.exceeded_buckets : 0,
    lastTriggered: raw.last_triggered?.trim() || undefined,
    hourlyHits: raw.hourly_hits ?? [],
  };
}

function velocityRulePath(id: string): string {
  return `/policy/velocity-rules/${encodeURIComponent(id)}`;
}

function describeVelocityRuleError(error: Error): string {
  if (error instanceof ApiError) {
    const detail = readApiErrorDetail(error);
    if (detail) {
      return detail;
    }
    return `Velocity rule request failed (status ${error.status}).`;
  }
  return error.message;
}

export function useVelocityRules() {
  return useQuery<VelocityRuleListResult>({
    queryKey: queryKeys.policies.velocityRules(),
    queryFn: async () => {
      const response = await get<{
        items?: BackendVelocityRule[];
        count?: number;
        limit?: number;
        upgrade_url?: string;
      }>("/policy/velocity-rules");
      const items = (response.items ?? []).map(mapVelocityRule).filter((rule) => rule.id !== "");
      return {
        items,
        count: typeof response.count === "number" ? response.count : items.length,
        limit: typeof response.limit === "number" ? response.limit : 0,
        upgradeUrl: response.upgrade_url?.trim() || undefined,
      };
    },
    staleTime: 10_000,
  });
}

export function useVelocityRuleStats() {
  return useQuery<VelocityRuleStatsResult>({
    queryKey: queryKeys.policies.velocityRuleStats(),
    queryFn: async () => {
      const response = await get<{
        items?: BackendVelocityRuleStats[];
        top_rules?: BackendVelocityRuleStats[];
        generated_at?: string;
      }>("/policy/velocity-rules/stats");
      return {
        items: (response.items ?? []).map(mapVelocityRuleStats).filter((item) => item.id !== ""),
        topRules: (response.top_rules ?? []).map(mapVelocityRuleStats).filter((item) => item.id !== ""),
        generatedAt: response.generated_at?.trim() || undefined,
      };
    },
    staleTime: 10_000,
  });
}

export function useCreateVelocityRule() {
  const queryClient = useQueryClient();
  return useMutation<VelocityRule, Error, VelocityRuleInput>({
    mutationFn: async (input) =>
      mapVelocityRule(await post<BackendVelocityRule>("/policy/velocity-rules", input)),
    onSuccess: () => {
      useToastStore.getState().addToast({ type: "success", title: "Velocity rule created" });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.velocityRules() });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.velocityRuleStats() });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.rules() });
    },
    onError: (error) => {
      useToastStore.getState().addToast({
        type: "error",
        title: "Failed to create velocity rule",
        description: describeVelocityRuleError(error),
      });
    },
  });
}

export function useUpdateVelocityRule() {
  const queryClient = useQueryClient();
  return useMutation<VelocityRule, Error, VelocityRuleInput>({
    mutationFn: async (input) =>
      mapVelocityRule(await put<BackendVelocityRule>(velocityRulePath(input.id), input)),
    onSuccess: () => {
      useToastStore.getState().addToast({ type: "success", title: "Velocity rule updated" });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.velocityRules() });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.velocityRuleStats() });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.rules() });
    },
    onError: (error) => {
      useToastStore.getState().addToast({
        type: "error",
        title: "Failed to update velocity rule",
        description: describeVelocityRuleError(error),
      });
    },
  });
}

export function useDeleteVelocityRule() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { id: string }>({
    mutationFn: async ({ id }) => {
      await del<void>(velocityRulePath(id));
    },
    onSuccess: () => {
      useToastStore.getState().addToast({ type: "success", title: "Velocity rule deleted" });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.velocityRules() });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.velocityRuleStats() });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.rules() });
    },
    onError: (error) => {
      useToastStore.getState().addToast({
        type: "error",
        title: "Failed to delete velocity rule",
        description: describeVelocityRuleError(error),
      });
    },
  });
}

export function usePolicySnapshots() {
  return useQuery<ApiResponse<PolicySnapshotSummary[]>>({
    queryKey: queryKeys.policies.snapshots(),
    queryFn: async () => {
      const res = await get<{ items: BackendPolicySnapshotSummary[] }>("/policy/bundles/snapshots");
      return { items: (res.items ?? []).map(mapPolicySnapshotSummary) };
    },
    staleTime: 30_000,
  });
}

export function usePolicySnapshot(id: string | null) {
  return useQuery<PolicySnapshot>({
    queryKey: queryKeys.policies.snapshot(id),
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
// Policy config / lockdown
// ---------------------------------------------------------------------------

export const POLICY_CONFIG_SUPPORTED =
  import.meta.env.VITE_POLICY_CONFIG_SUPPORTED === "true";

export interface PolicyConfig {
  lockdownActive: boolean;
  lockdownReason?: string;
  defaultDecision: string;
  maxEvalTimeMs: number;
  lockdown?: boolean;
  lockdownBy?: string;
  lockdownAt?: string;
  defaultStance?: string;
}

const DEFAULT_POLICY_CONFIG: PolicyConfig = {
  lockdownActive: false,
  defaultDecision: "deny",
  maxEvalTimeMs: 500,
};

export function usePolicyConfig() {
  return useQuery<PolicyConfig>({
    queryKey: queryKeys.policies.config(),
    queryFn: async () => {
      if (!POLICY_CONFIG_SUPPORTED) return DEFAULT_POLICY_CONFIG;
      const res = await get<PolicyConfig>("/policy/config");
      return res;
    },
    staleTime: 30_000,
  });
}

export function useUpdatePolicyConfig() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, Partial<PolicyConfig>>({
    mutationFn: (config) => {
      if (!POLICY_CONFIG_SUPPORTED) return Promise.resolve();
      return put<void>("/policy/config", config);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.config() });
    },
  });
}

export function useActivateLockdown() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { reason: string }>({
    mutationFn: ({ reason }) => {
      logger.info("policies", "Activating lockdown", { reason });
      return post<void>("/policy/lockdown", { reason });
    },
    onSuccess: () => {
      logger.info("policies", "Lockdown activated");
      useToastStore.getState().addToast({ type: "warning", title: "Lockdown activated" });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.config() });
    },
    onError: (err) => {
      logger.error("policies", "Lockdown activation failed", { error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Lockdown failed", description: err.message });
    },
  });
}

export function useDeactivateLockdown() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, void>({
    mutationFn: () => {
      logger.info("policies", "Deactivating lockdown");
      return del<void>("/policy/lockdown");
    },
    onSuccess: () => {
      logger.info("policies", "Lockdown deactivated");
      useToastStore.getState().addToast({ type: "success", title: "Lockdown deactivated" });
      queryClient.invalidateQueries({ queryKey: queryKeys.policies.config() });
    },
    onError: (err) => {
      logger.error("policies", "Lockdown deactivation failed", { error: err.message });
      useToastStore.getState().addToast({ type: "error", title: "Deactivation failed", description: err.message });
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

  const pending = useMemo<PendingPolicyChange[]>(() => {
    const result: PendingPolicyChange[] = [];
    for (const b of bundles) {
      const updatedMs = b.updatedAt ? new Date(b.updatedAt).getTime() : 0;
      const publishedMs = b.publishedAt ? new Date(b.publishedAt).getTime() : 0;

      if (b.rules.length > 0 && (!b.publishedAt || updatedMs > publishedMs + 1000)) {
        const changeSummary = !b.publishedAt
          ? `${b.rules.length} new rule${b.rules.length !== 1 ? "s" : ""}`
          : `${b.rules.length} rule${b.rules.length !== 1 ? "s" : ""} modified`;
        result.push({ bundle: b, changeSummary });
      }
    }
    return result;
  }, [bundles]);

  return { pending, isLoading, isError };
}

/** @internal exported for unit tests */
// ---------------------------------------------------------------------------
// Policy Replay
// ---------------------------------------------------------------------------

export function useReplayPolicy() {
  return useMutation<PolicyReplayResponse, Error, PolicyReplayRequest>({
    mutationFn: (input) => {
      logger.info("policies", "Running policy replay", {
        from: input.from,
        to: input.to,
        useCurrentPolicy: input.use_current_policy,
        maxJobs: input.max_jobs,
      });
      return post<PolicyReplayResponse>("/policy/replay", input);
    },
    onSuccess: (data) => {
      logger.info("policies", "Policy replay completed", {
        replayId: data.replay_id,
        totalJobs: data.summary.total_jobs,
        escalated: data.summary.escalated,
        relaxed: data.summary.relaxed,
      });
      useToastStore.getState().addToast({
        type: "success",
        title: "Replay completed",
        description: `${data.summary.total_jobs} jobs replayed — ${data.summary.escalated} escalated, ${data.summary.relaxed} relaxed`,
      });
    },
    onError: (err) => {
      logger.error("policies", "Policy replay failed", { error: err.message });
      useToastStore.getState().addToast({
        type: "error",
        title: "Replay failed",
        description: err.message,
      });
    },
  });
}

// ---------------------------------------------------------------------------
// Policy Analytics
// ---------------------------------------------------------------------------

export function usePolicyAnalytics() {
  return useMutation<PolicyAnalyticsResponse, Error, PolicyAnalyticsRequest>({
    mutationFn: (input) => {
      logger.info("policies", "Running policy analytics", {
        from: input.from,
        to: input.to,
        ruleFilter: input.rule_filter,
      });
      return post<PolicyAnalyticsResponse>("/policy/analytics", input);
    },
    onSuccess: (data) => {
      logger.info("policies", "Policy analytics completed", {
        totalRules: data.summary.total_rules,
        totalHits: data.summary.total_hits,
        totalOverrides: data.summary.total_overrides,
      });
      useToastStore.getState().addToast({
        type: "success",
        title: "Analysis completed",
        description: `${data.summary.total_rules} rules analyzed — ${data.summary.total_overrides} overrides found`,
      });
    },
    onError: (err) => {
      logger.error("policies", "Policy analytics failed", { error: err.message });
      useToastStore.getState().addToast({
        type: "error",
        title: "Analysis failed",
        description: err.message,
      });
    },
  });
}

export const __policiesInternal = {
  readPolicyBundleContent,
  policyBundlePath,
  policyBundleRulePath,
  policyBundleSimulatePath,
  describeBundleUpdateError,
  DEFAULT_POLICY_CONFIG,
};
