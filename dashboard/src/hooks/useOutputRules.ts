import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import YAML from "yaml";
import { get, put } from "../api/client";
import { logger } from "../lib/logger";
import { useToastStore } from "../state/toast";
import type { OutputRule, OutputRuleAuditEntry, OutputRuleFinding } from "../types/policy";
import { encodePolicyBundleId } from "./usePolicies";

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return undefined;
  }
  return value as Record<string, unknown>;
}

function asString(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function asStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value
    .map((item) => (typeof item === "string" ? item.trim() : ""))
    .filter(Boolean);
}

function asNumber(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) {
      return parsed;
    }
  }
  return undefined;
}

function asBool(value: unknown, fallback = false): boolean {
  if (typeof value === "boolean") return value;
  if (typeof value === "string") {
    const normalized = value.trim().toLowerCase();
    if (["1", "true", "yes", "on"].includes(normalized)) return true;
    if (["0", "false", "no", "off"].includes(normalized)) return false;
  }
  return fallback;
}

function mapOutputRule(raw: Record<string, unknown>): OutputRule {
  const id = asString(raw.id).trim();
  const patterns = asStringArray(raw.patterns);
  const patternPreview = asString(raw.pattern_preview).trim() || patterns[0] || "";
  return {
    id,
    description: asString(raw.description).trim() || undefined,
    topics: asStringArray(raw.topics),
    scanners: asStringArray(raw.scanners),
    patterns,
    patternPreview: patternPreview || undefined,
    decision: asString(raw.decision).trim().toLowerCase() || "allow",
    severity: asString(raw.severity).trim().toLowerCase() || "low",
    enabled: asBool(raw.enabled, true),
    reason: asString(raw.reason).trim() || undefined,
    match: asRecord(raw.match),
    source: asRecord(raw.source),
    lastTriggered: asString(raw.last_triggered).trim() || undefined,
    triggerCount24h: asNumber(raw.trigger_count_24h),
  };
}

function readPolicyBundleContent(raw: unknown): string {
  if (typeof raw === "string") return raw;
  const record = asRecord(raw);
  if (!record) return "";
  const content = asString(record.content).trim();
  if (content) return content;
  const policy = asString(record.policy).trim();
  if (policy) return policy;
  return asString(record.data).trim();
}

function toStringList(raw: string): string[] {
  return raw
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

export interface OutputRuleDraftInput {
  id: string;
  description?: string;
  pattern: string;
  decision: string;
  severity: string;
  enabled: boolean;
  reason?: string;
  topics: string[];
  scanners: string[];
}

export interface UpsertOutputRuleInput {
  bundleId: string;
  existingRuleId?: string;
  draft: OutputRuleDraftInput;
}

function buildWritableOutputRule(draft: OutputRuleDraftInput): Record<string, unknown> {
  return {
    id: draft.id.trim(),
    description: draft.description?.trim() || undefined,
    decision: draft.decision.trim().toLowerCase(),
    severity: draft.severity.trim().toLowerCase(),
    enabled: draft.enabled,
    reason: draft.reason?.trim() || undefined,
    match: {
      topics: draft.topics,
      scanners: draft.scanners,
      content_patterns: [draft.pattern.trim()],
    },
  };
}

function mapOutputRuleFinding(raw: unknown): OutputRuleFinding | null {
  const record = asRecord(raw);
  if (!record) return null;
  const detail = asString(record.detail).trim();
  if (!detail) return null;
  return {
    type: asString(record.type).trim() || "unknown",
    severity: asString(record.severity).trim() || "low",
    detail,
    scanner: asString(record.scanner).trim() || undefined,
    confidence: asNumber(record.confidence),
    matchedPattern: asString(record.matched_pattern).trim() || undefined,
  };
}

function mapOutputRuleAuditEntry(raw: Record<string, unknown>): OutputRuleAuditEntry | null {
  const payload = asRecord(raw.payload);
  const findingsRaw = raw.findings ?? payload?.findings;
  const findings = Array.isArray(findingsRaw)
    ? findingsRaw.map(mapOutputRuleFinding).filter((value): value is OutputRuleFinding => value !== null)
    : [];

  const timestamp = asString(raw.created_at).trim() || asString(raw.timestamp).trim();
  const ruleID = asString(raw.rule_id).trim() || asString(raw.resource_id).trim();
  const jobID =
    asString(raw.job_id).trim() ||
    asString(raw.resource_name).trim() ||
    asString(payload?.job_id).trim();
  if (!timestamp || !jobID) {
    return null;
  }
  return {
    id: asString(raw.id).trim() || `${jobID}-${timestamp}`,
    jobId: jobID,
    ruleId: ruleID,
    timestamp,
    decision: asString(raw.decision).trim() || asString(payload?.decision).trim() || undefined,
    reason: asString(raw.reason).trim() || asString(raw.message).trim() || undefined,
    phase: asString(raw.phase).trim() || asString(payload?.phase).trim() || undefined,
    findings,
    originalPtr: asString(raw.original_ptr).trim() || asString(payload?.original_ptr).trim() || undefined,
    redactedPtr: asString(raw.redacted_ptr).trim() || asString(payload?.redacted_ptr).trim() || undefined,
  };
}

function buildOutputAuditPath(ruleId: string): string {
  const params = new URLSearchParams();
  params.set("type", "output");
  params.set("rule_id", ruleId);
  return `/policy/audit?${params.toString()}`;
}

export function useOutputRules() {
  return useQuery<OutputRule[]>({
    queryKey: ["output-rules"],
    queryFn: async () => {
      const response = await get<{ items?: Record<string, unknown>[] }>("/policy/output/rules");
      const items = response.items ?? [];
      return items.map(mapOutputRule).filter((rule) => rule.id !== "");
    },
    staleTime: 10_000,
  });
}

export function useToggleOutputRule() {
  const queryClient = useQueryClient();
  return useMutation<
    { id: string; enabled: boolean; bundle_id?: string; updated_at?: string },
    Error,
    { id: string; enabled: boolean },
    { previousRules?: OutputRule[] }
  >({
    mutationFn: async ({ id, enabled }) => {
      logger.info("policies", "Toggling output rule", { id, enabled });
      return put<{ id: string; enabled: boolean; bundle_id?: string; updated_at?: string }>(
        `/policy/output/rules/${encodeURIComponent(id)}`,
        { enabled },
      );
    },
    onMutate: async ({ id, enabled }) => {
      await queryClient.cancelQueries({ queryKey: ["output-rules"] });
      const previousRules = queryClient.getQueryData<OutputRule[]>(["output-rules"]);
      queryClient.setQueryData<OutputRule[]>(["output-rules"], (current = []) =>
        current.map((rule) => (rule.id === id ? { ...rule, enabled } : rule)),
      );
      return { previousRules };
    },
    onSuccess: (response, variables) => {
      queryClient.setQueryData<OutputRule[]>(["output-rules"], (current = []) =>
        current.map((rule) =>
          rule.id === variables.id ? { ...rule, enabled: response.enabled } : rule,
        ),
      );
      useToastStore.getState().addToast({ type: "success", title: "Output rule updated" });
    },
    onError: (error, variables, context) => {
      if (context?.previousRules) {
        queryClient.setQueryData(["output-rules"], context.previousRules);
      }
      logger.error("policies", "Toggle output rule failed", {
        id: variables.id,
        enabled: variables.enabled,
        error: error.message,
      });
      useToastStore.getState().addToast({
        type: "error",
        title: "Failed to update output rule",
        description: error.message,
      });
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ["output-rules"] });
    },
  });
}

export function useUpsertOutputRule() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, UpsertOutputRuleInput>({
    mutationFn: async ({ bundleId, existingRuleId, draft }) => {
      const normalizedBundleID = bundleId.trim();
      if (!normalizedBundleID) {
        throw new Error("bundle id is required");
      }
      const nextRuleID = draft.id.trim();
      if (!nextRuleID) {
        throw new Error("rule id is required");
      }
      const nextPattern = draft.pattern.trim();
      if (!nextPattern) {
        throw new Error("pattern is required");
      }

      const encodedBundleID = encodePolicyBundleId(normalizedBundleID);
      const bundle = await get<Record<string, unknown>>(`/policy/bundles/${encodedBundleID}`);
      const currentContent = readPolicyBundleContent(bundle);
      const parsed = YAML.parse(currentContent || "{}");
      const root =
        parsed && typeof parsed === "object" && !Array.isArray(parsed)
          ? (parsed as Record<string, unknown>)
          : {};

      const outputRulesRaw = Array.isArray(root.output_rules) ? [...root.output_rules] : [];
      const targetID = (existingRuleId || nextRuleID).trim();
      const replacement = buildWritableOutputRule(draft);
      const existingIndex = outputRulesRaw.findIndex((rule) => {
        const record = asRecord(rule);
        return record ? asString(record.id).trim() === targetID : false;
      });

      if (existingIndex >= 0) {
        outputRulesRaw[existingIndex] = replacement;
      } else {
        outputRulesRaw.push(replacement);
      }

      root.output_rules = outputRulesRaw;
      const nextContent = YAML.stringify(root);
      logger.info("policies", "Upserting output policy rule", {
        bundleId: normalizedBundleID,
        ruleId: nextRuleID,
      });
      await put(`/policy/bundles/${encodedBundleID}`, { content: nextContent });
    },
    onSuccess: (_result, variables) => {
      useToastStore.getState().addToast({
        type: "success",
        title: "Output rule saved",
      });
      queryClient.invalidateQueries({ queryKey: ["output-rules"] });
      queryClient.invalidateQueries({
        queryKey: ["policy-bundle", variables.bundleId.trim()],
      });
      queryClient.invalidateQueries({ queryKey: ["policy-bundles"] });
    },
    onError: (error, variables) => {
      logger.error("policies", "Output rule save failed", {
        bundleId: variables.bundleId,
        ruleId: variables.draft.id,
        error: error.message,
      });
      useToastStore.getState().addToast({
        type: "error",
        title: "Failed to save output rule",
        description: error.message,
      });
    },
  });
}

export function useOutputRuleAudit(ruleId: string, limit = 10) {
  return useQuery<OutputRuleAuditEntry[]>({
    queryKey: ["output-rule-audit", ruleId, limit],
    queryFn: async () => {
      const response = await get<{ items?: Record<string, unknown>[] }>(buildOutputAuditPath(ruleId));
      const items = response.items ?? [];
      return items
        .map(mapOutputRuleAuditEntry)
        .filter((entry): entry is OutputRuleAuditEntry => entry !== null)
        .slice(0, limit);
    },
    enabled: ruleId.trim() !== "",
    staleTime: 10_000,
  });
}

export const __outputRulesInternal = {
  mapOutputRule,
  mapOutputRuleAuditEntry,
  buildOutputAuditPath,
  readPolicyBundleContent,
  toStringList,
  buildWritableOutputRule,
};
