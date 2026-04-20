import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, del, get, post, put } from "../api/client";
import { mapJobDetail, mapJobRecord, type BackendJobDetail, type BackendJobRecord } from "../api/transform";
import type { ApiResponse, Job, OutputFinding } from "../api/types";
import { logger } from "../lib/logger";
import { queryKeys } from "../lib/queryKeys";
import { useToastStore } from "../state/toast";
import type { CustomPatternConfig, OutputPolicyConfig, OutputPolicyStats, ScannerOverride, TopicOverride } from "../types/settings";
import { DEFAULT_OUTPUT_POLICY_CONFIG } from "../types/settings";

export interface QuarantinedJobsFilters {
  limit?: number;
  cursor?: number;
}

function readOutputPolicyErrorDetails(error: ApiError): string | undefined {
  if (!error.body || typeof error.body !== "object") return undefined;
  const payload = error.body as Record<string, unknown>;
  return [payload.error, payload.message, payload.details]
    .map((value) => (typeof value === "string" ? value.trim() : ""))
    .find(Boolean);
}

export function describeOutputPolicyError(error: Error): string {
  if (error instanceof ApiError) {
    const details = readOutputPolicyErrorDetails(error);
    if (error.status === 400 || error.status === 422) {
      return details
        ? `Validation failed: ${details}`
        : "Validation failed while updating output policy.";
    }
    if (error.status === 409) {
      return details
        ? `Conflict while updating output policy: ${details}`
        : "Conflict while updating output policy. Refresh and retry.";
    }
    if (details) {
      return `Output policy request failed: ${details}`;
    }
    return `Output policy request failed (status ${error.status}).`;
  }
  return error.message;
}

function buildQuarantineParams(filters: QuarantinedJobsFilters): string {
  const params = new URLSearchParams();
  params.set("state", "OUTPUT_QUARANTINED");
  if (filters.limit !== undefined && filters.limit > 0) {
    params.set("limit", String(filters.limit));
  }
  if (filters.cursor !== undefined && filters.cursor > 0) {
    params.set("cursor", String(filters.cursor));
  }
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

export function useQuarantinedJobs(filters: QuarantinedJobsFilters = {}) {
  return useQuery<ApiResponse<Job[]>>({
    queryKey: queryKeys.jobs.quarantined(filters),
    queryFn: async () => {
      const res = await get<{ items: BackendJobRecord[]; next_cursor?: number | null }>(
        `/jobs${buildQuarantineParams(filters)}`,
      );
      return {
        items: (res.items ?? []).map(mapJobRecord),
        next_cursor: res.next_cursor ?? null,
      };
    },
    staleTime: 10_000,
  });
}

export function useReleaseQuarantinedJob() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (jobId) => {
      logger.info("output-policy", "Releasing quarantined job", { jobId });
      return post<void>(`/dlq/${encodeURIComponent(jobId)}/retry`);
    },
    onSuccess: (_, jobId) => {
      useToastStore.getState().addToast({ type: "success", title: "Released quarantined job" });
      queryClient.invalidateQueries({ queryKey: queryKeys.jobs.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.jobs.detail(jobId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.dlq.all });
    },
    onError: (err, jobId) => {
      const detail = describeOutputPolicyError(err);
      logger.error("output-policy", "Release quarantined job failed", { jobId, error: detail });
      useToastStore.getState().addToast({
        type: "error",
        title: "Failed to release quarantined job",
        description: detail,
      });
    },
  });
}

export function useConfirmQuarantine() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: (jobId) => {
      logger.info("output-policy", "Confirming quarantine", { jobId });
      return del(`/dlq/${encodeURIComponent(jobId)}`);
    },
    onSuccess: (_, jobId) => {
      useToastStore.getState().addToast({ type: "success", title: "Quarantine confirmed" });
      queryClient.invalidateQueries({ queryKey: queryKeys.jobs.all });
      queryClient.invalidateQueries({ queryKey: queryKeys.jobs.detail(jobId) });
      queryClient.invalidateQueries({ queryKey: queryKeys.dlq.all });
    },
    onError: (err, jobId) => {
      const detail = describeOutputPolicyError(err);
      logger.error("output-policy", "Confirm quarantine failed", { jobId, error: detail });
      useToastStore.getState().addToast({
        type: "error",
        title: "Failed to confirm quarantine",
        description: detail,
      });
    },
  });
}

export function useOutputFindings(jobId: string) {
  return useQuery<OutputFinding[]>({
    queryKey: queryKeys.jobs.outputFindings(jobId),
    queryFn: async () => {
      const res = await get<BackendJobDetail>(`/jobs/${encodeURIComponent(jobId)}`);
      const job = mapJobDetail(res);
      return job.output_safety?.findings ?? [];
    },
    enabled: !!jobId,
    staleTime: 5_000,
  });
}

interface RawOutputPolicyStats {
  total_checks_24h?: number;
  quarantined_24h?: number;
  avg_latency_ms?: number;
  last_check_at?: string;
}

function parseBool(value: unknown): boolean | undefined {
  if (typeof value === "boolean") return value;
  if (typeof value !== "string") return undefined;
  switch (value.trim().toLowerCase()) {
    case "true":
    case "1":
    case "yes":
    case "on":
      return true;
    case "false":
    case "0":
    case "no":
    case "off":
      return false;
    default:
      return undefined;
  }
}

function parseNum(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value !== "string") return undefined;
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function toObject(value: unknown): Record<string, unknown> | undefined {
  if (value && typeof value === "object") {
    return value as Record<string, unknown>;
  }
  return undefined;
}

function uniqueStrings(values: string[]): string[] {
  return Array.from(new Set(values.map((v) => v.trim()).filter(Boolean)));
}

function parseTopicOverride(raw: unknown): TopicOverride | null {
  const obj = toObject(raw);
  if (!obj) return null;
  const topicPattern =
    (typeof obj.topic_pattern === "string" ? obj.topic_pattern : undefined) ??
    (typeof obj.topicPattern === "string" ? obj.topicPattern : undefined) ??
    (typeof obj.topic === "string" ? obj.topic : undefined) ??
    "";
  if (!topicPattern.trim()) return null;
  const failModeRaw =
    (typeof obj.fail_mode === "string" ? obj.fail_mode : undefined) ??
    (typeof obj.failMode === "string" ? obj.failMode : undefined) ??
    "open";
  const scannersRaw = Array.isArray(obj.scanners)
    ? obj.scanners
    : Array.isArray(obj.detectors)
      ? obj.detectors
      : [];
  return {
    topicPattern: topicPattern.trim(),
    enabled: parseBool(obj.enabled) ?? true,
    failMode: failModeRaw === "closed" ? "closed" : "open",
    scanners: uniqueStrings(scannersRaw.map((v) => String(v))),
  };
}

function parseTopicOverrides(raw?: Record<string, unknown>): TopicOverride[] {
  if (!raw) return [];
  const outputSafety = toObject(raw.output_safety);
  const outputPolicy = toObject(raw.output_policy);
  const candidates = [
    outputPolicy?.topic_overrides,
    outputPolicy?.topicOverrides,
    outputSafety?.topic_overrides,
    raw.output_policy_topic_overrides,
    raw.output_safety_topic_overrides,
  ];
  for (const candidate of candidates) {
    if (!Array.isArray(candidate)) continue;
    const overrides = candidate
      .map(parseTopicOverride)
      .filter((entry): entry is TopicOverride => entry !== null);
    if (overrides.length > 0) return overrides;
  }
  return [];
}

function parseScannerOverride(raw: unknown): ScannerOverride | null {
  const obj = toObject(raw);
  if (!obj) return null;
  const id = typeof obj.id === "string" ? obj.id.trim() : "";
  if (!id) return null;
  return {
    id,
    enabled: parseBool(obj.enabled) ?? undefined,
    action: typeof obj.action === "string" ? obj.action : undefined,
    confidence: parseNum(obj.confidence) ?? (typeof obj.confidence === "number" ? obj.confidence : undefined),
    enabledTypes: Array.isArray(obj.enabledTypes)
      ? obj.enabledTypes.filter((v): v is string => typeof v === "string")
      : Array.isArray(obj.enabled_types)
        ? (obj.enabled_types as unknown[]).filter((v): v is string => typeof v === "string")
        : undefined,
  };
}

function parseScannerOverrides(raw?: Record<string, unknown>): ScannerOverride[] {
  if (!raw) return [];
  const outputPolicy = toObject(raw.output_policy);
  const candidates = [
    outputPolicy?.scanner_overrides,
    outputPolicy?.scannerOverrides,
    raw.scanner_overrides,
    raw.scannerOverrides,
  ];
  for (const candidate of candidates) {
    if (!Array.isArray(candidate)) continue;
    const overrides = candidate
      .map(parseScannerOverride)
      .filter((entry): entry is ScannerOverride => entry !== null);
    if (overrides.length > 0) return overrides;
  }
  return [];
}

function parseCustomPattern(raw: unknown): CustomPatternConfig | null {
  const obj = toObject(raw);
  if (!obj) return null;
  const id = typeof obj.id === "string" ? obj.id.trim() : "";
  const name = typeof obj.name === "string" ? obj.name.trim() : "";
  if (!id || !name) return null;
  return {
    id,
    name,
    regex: typeof obj.regex === "string" ? obj.regex : "",
    category: typeof obj.category === "string" ? obj.category : "",
    action: typeof obj.action === "string" ? obj.action : "quarantine",
    enabled: parseBool(obj.enabled) ?? true,
  };
}

function parseCustomPatterns(raw?: Record<string, unknown>): CustomPatternConfig[] {
  if (!raw) return [];
  const outputPolicy = toObject(raw.output_policy);
  const candidates = [
    outputPolicy?.custom_patterns,
    outputPolicy?.customPatterns,
    raw.custom_patterns,
    raw.customPatterns,
  ];
  for (const candidate of candidates) {
    if (!Array.isArray(candidate)) continue;
    const patterns = candidate
      .map(parseCustomPattern)
      .filter((entry): entry is CustomPatternConfig => entry !== null);
    if (patterns.length > 0) return patterns;
  }
  return [];
}

function parseOutputPolicyConfig(raw?: Record<string, unknown>): OutputPolicyConfig {
  if (!raw) return DEFAULT_OUTPUT_POLICY_CONFIG;
  const outputSafety = toObject(raw.output_safety);
  const outputPolicy = toObject(raw.output_policy);

  const enabled =
    parseBool(outputSafety?.enabled) ??
    parseBool(outputPolicy?.enabled) ??
    parseBool(raw.output_policy_enabled) ??
    parseBool(raw.outputPolicyEnabled) ??
    parseBool(raw.OUTPUT_POLICY_ENABLED) ??
    DEFAULT_OUTPUT_POLICY_CONFIG.enabled;

  const failModeRaw =
    (typeof outputPolicy?.fail_mode === "string" ? outputPolicy.fail_mode : undefined) ??
    (typeof raw.output_policy_fail_mode === "string" ? raw.output_policy_fail_mode : undefined) ??
    (typeof raw.outputPolicyFailMode === "string" ? raw.outputPolicyFailMode : undefined) ??
    DEFAULT_OUTPUT_POLICY_CONFIG.failMode;

  const failureActionRaw =
    (typeof outputSafety?.failure_action === "string" ? outputSafety.failure_action : undefined) ??
    (typeof outputPolicy?.failure_action === "string" ? outputPolicy.failure_action : undefined) ??
    (typeof raw.output_safety_failure_action === "string" ? raw.output_safety_failure_action : undefined) ??
    DEFAULT_OUTPUT_POLICY_CONFIG.failureAction;

  return {
    enabled,
    failMode: failModeRaw === "closed" ? "closed" : "open",
    scanTimeoutMs:
      parseNum(outputSafety?.scan_timeout_ms) ??
      parseNum(outputPolicy?.scan_timeout_ms) ??
      parseNum(raw.output_safety_scan_timeout_ms) ??
      parseNum(raw.output_policy_scan_timeout_ms) ??
      DEFAULT_OUTPUT_POLICY_CONFIG.scanTimeoutMs,
    maxPayloadKb:
      parseNum(outputSafety?.max_payload_kb) ??
      parseNum(outputPolicy?.max_payload_kb) ??
      parseNum(raw.output_safety_max_payload_kb) ??
      parseNum(raw.output_policy_max_payload_kb) ??
      DEFAULT_OUTPUT_POLICY_CONFIG.maxPayloadKb,
    failureAction: failureActionRaw === "deny" ? "deny" : "allow",
    topicOverrides: parseTopicOverrides(raw),
    scannerOverrides: parseScannerOverrides(raw),
    customPatterns: parseCustomPatterns(raw),
  };
}

function buildTopicOverrides(overrides: TopicOverride[]): Array<Record<string, unknown>> {
  return overrides.map((entry) => ({
    topic_pattern: entry.topicPattern,
    enabled: entry.enabled,
    fail_mode: entry.failMode,
    scanners: uniqueStrings(entry.scanners),
  }));
}

function buildScannerOverrides(overrides: ScannerOverride[]): Array<Record<string, unknown>> {
  return overrides.map((entry) => ({
    id: entry.id,
    enabled: entry.enabled,
    action: entry.action,
    confidence: entry.confidence,
    enabled_types: entry.enabledTypes,
  }));
}

function buildCustomPatterns(patterns: CustomPatternConfig[]): Array<Record<string, unknown>> {
  return patterns.map((p) => ({
    id: p.id,
    name: p.name,
    regex: p.regex,
    category: p.category,
    action: p.action,
    enabled: p.enabled,
  }));
}

function mergeOutputPolicyConfig(
  current: Record<string, unknown> | undefined,
  next: OutputPolicyConfig,
): Record<string, unknown> {
  const existing = { ...(current ?? {}) };
  const currentOutputPolicy = toObject(existing.output_policy) ?? {};
  const currentOutputSafety = toObject(existing.output_safety) ?? {};
  const topicOverrides = buildTopicOverrides(next.topicOverrides);
  const scannerOverrides = buildScannerOverrides(next.scannerOverrides);
  const customPatternsData = buildCustomPatterns(next.customPatterns);
  return {
    ...existing,
    output_safety: {
      ...currentOutputSafety,
      enabled: next.enabled,
      scan_timeout_ms: next.scanTimeoutMs,
      max_payload_kb: next.maxPayloadKb,
      failure_action: next.failureAction,
      topic_overrides: topicOverrides,
    },
    output_policy: {
      ...currentOutputPolicy,
      enabled: next.enabled,
      fail_mode: next.failMode,
      scan_timeout_ms: next.scanTimeoutMs,
      max_payload_kb: next.maxPayloadKb,
      failure_action: next.failureAction,
      topic_overrides: topicOverrides,
      scanner_overrides: scannerOverrides,
      custom_patterns: customPatternsData,
    },
    output_policy_enabled: next.enabled,
    output_policy_fail_mode: next.failMode,
    output_policy_scan_timeout_ms: next.scanTimeoutMs,
    output_policy_max_payload_kb: next.maxPayloadKb,
    output_safety_failure_action: next.failureAction,
    output_policy_topic_overrides: topicOverrides,
    scanner_overrides: scannerOverrides,
    custom_patterns: customPatternsData,
    OUTPUT_POLICY_ENABLED: next.enabled ? "true" : "false",
  };
}

function buildScopedConfigPayload(data: Record<string, unknown>): Record<string, unknown> {
  return {
    scope: "system",
    scope_id: "default",
    data,
    meta: { scope: "output_policy", source: "dashboard" },
  };
}

async function persistOutputPolicyConfig(data: Record<string, unknown>): Promise<void> {
  const payload = buildScopedConfigPayload(data);
  try {
    await put<void>("/config", payload);
  } catch (err) {
    if (err instanceof ApiError && (err.status === 404 || err.status === 405)) {
      await post<void>("/config", payload);
      return;
    }
    throw err;
  }
}

async function fetchSystemConfig(): Promise<Record<string, unknown>> {
  try {
    return await get<Record<string, unknown>>("/config?scope=system&scope_id=default");
  } catch (err) {
    if (err instanceof ApiError && (err.status === 404 || err.status === 405)) {
      return get<Record<string, unknown>>("/config");
    }
    throw err;
  }
}

async function fetchOutputPolicyConfigRaw(): Promise<Record<string, unknown>> {
  return fetchSystemConfig();
}

function mapOutputPolicyStats(raw?: RawOutputPolicyStats): OutputPolicyStats {
  return {
    totalChecks24h: raw?.total_checks_24h ?? 0,
    quarantined24h: raw?.quarantined_24h ?? 0,
    avgLatencyMs: raw?.avg_latency_ms ?? 0,
    lastCheckAt: raw?.last_check_at,
  };
}

export function useOutputPolicyConfig() {
  return useQuery<OutputPolicyConfig>({
    queryKey: queryKeys.outputPolicy.config(),
    queryFn: async () => {
      const raw = await fetchOutputPolicyConfigRaw();
      return parseOutputPolicyConfig(raw);
    },
    staleTime: 10_000,
  });
}

export function useUpdateOutputPolicy() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, OutputPolicyConfig>({
    mutationFn: async (next) => {
      const current = await fetchSystemConfig();
      const merged = mergeOutputPolicyConfig(current, next);
      await persistOutputPolicyConfig(merged);
    },
    onSuccess: () => {
      useToastStore.getState().addToast({
        type: "success",
        title: "Output Safety settings saved",
      });
      queryClient.invalidateQueries({ queryKey: queryKeys.outputPolicy.config() });
      queryClient.invalidateQueries({ queryKey: queryKeys.config.system() });
      queryClient.invalidateQueries({ queryKey: queryKeys.status.overview() });
      queryClient.invalidateQueries({ queryKey: queryKeys.outputPolicy.stats() });
    },
    onError: (err) => {
      const detail = describeOutputPolicyError(err);
      logger.error("output-policy", "failed to update output policy config", {
        error: detail,
      });
      useToastStore.getState().addToast({
        type: "error",
        title: "Failed to save Output Safety settings",
        description: detail,
      });
    },
  });
}

export function useOutputPolicyStats() {
  return useQuery<OutputPolicyStats>({
    queryKey: queryKeys.outputPolicy.stats(),
    queryFn: async () => {
      try {
        const raw = await get<RawOutputPolicyStats>("/policy/output/stats");
        return mapOutputPolicyStats(raw);
      } catch (err) {
        if (
          err instanceof ApiError &&
          (err.status === 404 || err.status === 405 || err.status === 501)
        ) {
          return mapOutputPolicyStats({});
        }
        throw err;
      }
    },
    staleTime: 25_000,
    refetchInterval: 30_000,
  });
}

/** @internal exported for unit tests */
export const __outputPolicyInternal = {
  buildQuarantineParams,
  parseOutputPolicyConfig,
  mergeOutputPolicyConfig,
  mapOutputPolicyStats,
  describeOutputPolicyError,
};
