import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { Activity, AlertTriangle, ShieldCheck, ShieldOff } from "lucide-react";
import { ApiError, get, post, put } from "../../api/client";
import { MetricCard } from "../../components/MetricCard";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../components/ui/Card";
import { Input } from "../../components/ui/Input";
import { Select } from "../../components/ui/Select";
import { usePolicyAudit, usePolicyBundles } from "../../hooks/usePolicies";
import { usePageTitle } from "../../hooks/usePageTitle";
import { useConfig } from "../../hooks/useSettings";
import { useStatus } from "../../hooks/useStatus";
import { logger } from "../../lib/logger";
import { useToastStore } from "../../state/toast";
import type { PolicyRule } from "../../api/types";
import { ErrorBanner } from "../../components/ui/ErrorBanner";

type FailMode = "open" | "closed";
type FailureAction = "allow" | "deny";

interface TopicOverride {
  topicPattern: string;
  enabled: boolean;
  failMode: FailMode;
  scanners: string[];
}

interface OutputSafetyFormState {
  enabled: boolean;
  failMode: FailMode;
  scanTimeoutMs: number;
  maxPayloadKb: number;
  failureAction: FailureAction;
  topicOverrides: TopicOverride[];
}

interface OutputPolicyStatus {
  enabled?: boolean;
  fail_mode?: string;
  kernel_connected?: boolean;
  last_check_at?: string;
}

interface OutputPolicyStats {
  total_checks_24h?: number;
  quarantined_24h?: number;
  avg_latency_ms?: number;
  last_check_at?: string;
}

interface OutputAuditEntry {
  id: string;
  action: string;
  actor: string;
  timestamp: string;
  bundleId: string;
  details?: Record<string, unknown>;
}

const DEFAULT_FORM_STATE: OutputSafetyFormState = {
  enabled: false,
  failMode: "open",
  scanTimeoutMs: 5000,
  maxPayloadKb: 512,
  failureAction: "allow",
  topicOverrides: [],
};

const DEFAULT_OVERRIDE_DRAFT: TopicOverride = {
  topicPattern: "",
  enabled: true,
  failMode: "open",
  scanners: ["secret"],
};

const SCANNER_OPTIONS: Array<{ value: string; label: string; description: string }> = [
  { value: "secret", label: "Secret Leak", description: "Credentials, API keys, private keys" },
  { value: "pii", label: "PII", description: "Email, SSN, payment card, phone data" },
  { value: "injection", label: "Injection", description: "SQL/shell/prompt injection patterns" },
  { value: "content_patterns", label: "Custom Patterns", description: "Regex-based content checks" },
];

function uniqueStrings(values: string[]): string[] {
  return Array.from(new Set(values.map((v) => v.trim()).filter(Boolean)));
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

function parseOutputSafetyConfig(raw?: Record<string, unknown>): OutputSafetyFormState {
  if (!raw) return DEFAULT_FORM_STATE;

  const outputSafety = toObject(raw.output_safety);
  const outputPolicy = toObject(raw.output_policy);

  const enabled =
    parseBool(outputSafety?.enabled) ??
    parseBool(outputPolicy?.enabled) ??
    parseBool(raw.output_policy_enabled) ??
    parseBool(raw.outputPolicyEnabled) ??
    parseBool(raw.OUTPUT_POLICY_ENABLED) ??
    DEFAULT_FORM_STATE.enabled;

  const failModeRaw =
    (typeof outputPolicy?.fail_mode === "string" ? outputPolicy.fail_mode : undefined) ??
    (typeof raw.output_policy_fail_mode === "string" ? raw.output_policy_fail_mode : undefined) ??
    (typeof raw.outputPolicyFailMode === "string" ? raw.outputPolicyFailMode : undefined) ??
    DEFAULT_FORM_STATE.failMode;

  const failureActionRaw =
    (typeof outputSafety?.failure_action === "string" ? outputSafety.failure_action : undefined) ??
    (typeof outputPolicy?.failure_action === "string" ? outputPolicy.failure_action : undefined) ??
    (typeof raw.output_safety_failure_action === "string" ? raw.output_safety_failure_action : undefined) ??
    DEFAULT_FORM_STATE.failureAction;

  return {
    enabled,
    failMode: failModeRaw === "closed" ? "closed" : "open",
    scanTimeoutMs:
      parseNum(outputSafety?.scan_timeout_ms) ??
      parseNum(outputPolicy?.scan_timeout_ms) ??
      parseNum(raw.output_safety_scan_timeout_ms) ??
      parseNum(raw.output_policy_scan_timeout_ms) ??
      DEFAULT_FORM_STATE.scanTimeoutMs,
    maxPayloadKb:
      parseNum(outputSafety?.max_payload_kb) ??
      parseNum(outputPolicy?.max_payload_kb) ??
      parseNum(raw.output_safety_max_payload_kb) ??
      parseNum(raw.output_policy_max_payload_kb) ??
      DEFAULT_FORM_STATE.maxPayloadKb,
    failureAction: failureActionRaw === "deny" ? "deny" : "allow",
    topicOverrides: parseTopicOverrides(raw),
  };
}

function buildTopicOverrides(overrides: TopicOverride[]): Array<Record<string, unknown>> {
  return overrides.map((entry) => ({
    topic_pattern: entry.topicPattern,
    enabled: entry.enabled,
    fail_mode: entry.failMode,
    scanners: entry.scanners,
  }));
}

function mergeOutputSafetyConfig(
  existingConfig: Record<string, unknown> | undefined,
  state: OutputSafetyFormState,
): Record<string, unknown> {
  const current = { ...(existingConfig ?? {}) };
  const currentOutputSafety = toObject(current.output_safety) ?? {};
  const currentOutputPolicy = toObject(current.output_policy) ?? {};
  const topicOverrides = buildTopicOverrides(state.topicOverrides);

  return {
    ...current,
    output_safety: {
      ...currentOutputSafety,
      enabled: state.enabled,
      scan_timeout_ms: state.scanTimeoutMs,
      max_payload_kb: state.maxPayloadKb,
      failure_action: state.failureAction,
      topic_overrides: topicOverrides,
    },
    output_policy: {
      ...currentOutputPolicy,
      enabled: state.enabled,
      fail_mode: state.failMode,
      scan_timeout_ms: state.scanTimeoutMs,
      max_payload_kb: state.maxPayloadKb,
      failure_action: state.failureAction,
      topic_overrides: topicOverrides,
    },
    output_policy_enabled: state.enabled,
    output_policy_fail_mode: state.failMode,
    output_policy_scan_timeout_ms: state.scanTimeoutMs,
    output_policy_max_payload_kb: state.maxPayloadKb,
    output_safety_failure_action: state.failureAction,
    output_policy_topic_overrides: topicOverrides,
    OUTPUT_POLICY_ENABLED: state.enabled ? "true" : "false",
  };
}

function wrapConfigPayload(
  data: Record<string, unknown>,
  scopeTag: string,
): Record<string, unknown> {
  return {
    scope: "system",
    scope_id: "default",
    data,
    meta: { scope: scopeTag, source: "dashboard" },
  };
}

async function persistConfig(payload: Record<string, unknown>): Promise<void> {
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

function isOutputRule(rule: PolicyRule): boolean {
  const match = rule.matchCriteria ?? {};
  return [
    "output_size_gt",
    "max_output_bytes",
    "scanners",
    "detectors",
    "content_patterns",
    "has_error",
  ].some((k) => k in match);
}

function isOutputDenial(entry: OutputAuditEntry): boolean {
  const action = entry.action.toLowerCase();
  const resourceType = String(entry.details?.resource_type ?? "").toLowerCase();
  const message = String(entry.details?.message ?? "").toLowerCase();
  const bundleId = entry.bundleId.toLowerCase();
  const deniedAction =
    action.includes("deny") || action.includes("reject") || action.includes("quarantine");
  const outputRelated =
    resourceType.includes("output") || message.includes("output") || bundleId.includes("output");
  return deniedAction && outputRelated;
}

function formatLastCheck(value?: string): string {
  if (!value) return "Never";
  const asDate = new Date(value);
  if (Number.isNaN(asDate.getTime())) return "Never";
  return asDate.toLocaleString();
}

export default function OutputSafetySettings() {
  usePageTitle("Settings - Output Safety");

  const queryClient = useQueryClient();
  const { data: configData, isLoading: configLoading, isError: configError, error: configErr, refetch: refetchConfig } = useConfig();
  const { data: status } = useStatus();
  const bundles = usePolicyBundles();
  const audit = usePolicyAudit();
  const addToast = useToastStore((s) => s.addToast);

  const [form, setForm] = useState<OutputSafetyFormState>(DEFAULT_FORM_STATE);
  const [overrideDraft, setOverrideDraft] = useState<TopicOverride>(DEFAULT_OVERRIDE_DRAFT);
  const [overrideFormOpen, setOverrideFormOpen] = useState(false);
  const [editingOverrideIndex, setEditingOverrideIndex] = useState<number | null>(null);

  useEffect(() => {
    setForm(parseOutputSafetyConfig(configData));
  }, [configData]);

  const statsQuery = useQuery<OutputPolicyStats>({
    queryKey: ["output-policy", "stats"],
    queryFn: async () => {
      try {
        return await get<OutputPolicyStats>("/policy/output/stats");
      } catch (err) {
        if (
          err instanceof ApiError &&
          (err.status === 404 || err.status === 405 || err.status === 501)
        ) {
          return {};
        }
        throw err;
      }
    },
    staleTime: 25_000,
    refetchInterval: 30_000,
  });

  const baseline = useMemo(() => parseOutputSafetyConfig(configData), [configData]);
  const isDirty = JSON.stringify(form) !== JSON.stringify(baseline);
  const overridesDirty =
    JSON.stringify(form.topicOverrides) !== JSON.stringify(baseline.topicOverrides);

  const saveMutation = useMutation<void, Error, OutputSafetyFormState>({
    mutationFn: async (nextState) => {
      const merged = mergeOutputSafetyConfig(
        toObject(configData),
        nextState,
      );
      await persistConfig(wrapConfigPayload(merged, "output_policy"));
    },
    onSuccess: () => {
      addToast({ type: "success", title: "Output Safety settings saved" });
      queryClient.invalidateQueries({ queryKey: ["config"] });
      queryClient.invalidateQueries({ queryKey: ["status"] });
      queryClient.invalidateQueries({ queryKey: ["output-policy", "stats"] });
    },
    onError: (err) => {
      logger.error("output-safety", "failed to save output safety settings", {
        error: err.message,
      });
      addToast({
        type: "error",
        title: "Failed to save Output Safety settings",
        description: err.message,
      });
    },
  });

  const saveOverridesMutation = useMutation<void, Error, TopicOverride[]>({
    mutationFn: async (overrides) => {
      const nextState: OutputSafetyFormState = {
        ...form,
        topicOverrides: overrides,
      };
      const merged = mergeOutputSafetyConfig(
        toObject(configData),
        nextState,
      );
      await persistConfig(wrapConfigPayload(merged, "output_policy"));
    },
    onSuccess: () => {
      addToast({ type: "success", title: "Topic overrides saved" });
      queryClient.invalidateQueries({ queryKey: ["config"] });
    },
    onError: (err) => {
      logger.error("output-safety", "failed to save topic overrides", {
        error: err.message,
      });
      addToast({
        type: "error",
        title: "Failed to save topic overrides",
        description: err.message,
      });
    },
  });

  const outputStatus = (status as { output_policy?: OutputPolicyStatus } | undefined)?.output_policy;
  const kernelConnected = outputStatus?.kernel_connected;
  const lastCheck = outputStatus?.last_check_at ?? statsQuery.data?.last_check_at;
  const effectiveEnabled = outputStatus?.enabled ?? form.enabled;

  const outputRuleSummary = useMemo(() => {
    const list = bundles.data?.items ?? [];
    let enabled = 0;
    let total = 0;
    for (const bundle of list) {
      for (const rule of bundle.rules) {
        if (!isOutputRule(rule)) continue;
        total += 1;
        if (rule.enabled !== false) enabled += 1;
      }
    }
    return { enabled, total };
  }, [bundles.data]);

  const recentDenials = useMemo(() => {
    const items = (audit.data?.items ?? []) as OutputAuditEntry[];
    return items
      .filter(isOutputDenial)
      .sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime())
      .slice(0, 5);
  }, [audit.data]);

  const totalChecks24h = statsQuery.data?.total_checks_24h ?? 0;
  const quarantined24h = statsQuery.data?.quarantined_24h ?? recentDenials.length;
  const avgLatencyMs = statsQuery.data?.avg_latency_ms ?? 0;

  const onSave = () => {
    saveMutation.mutate(form);
  };

  const resetOverrideEditor = () => {
    setOverrideDraft(DEFAULT_OVERRIDE_DRAFT);
    setOverrideFormOpen(false);
    setEditingOverrideIndex(null);
  };

  const startAddOverride = () => {
    setOverrideDraft(DEFAULT_OVERRIDE_DRAFT);
    setEditingOverrideIndex(null);
    setOverrideFormOpen(true);
  };

  const startEditOverride = (index: number) => {
    const existing = form.topicOverrides[index];
    if (!existing) return;
    setOverrideDraft(existing);
    setEditingOverrideIndex(index);
    setOverrideFormOpen(true);
  };

  const upsertOverride = () => {
    const topicPattern = overrideDraft.topicPattern.trim();
    if (!topicPattern) {
      addToast({
        type: "error",
        title: "Topic pattern is required",
        description: "Provide a topic pattern before saving the override.",
      });
      return;
    }

    const normalized: TopicOverride = {
      topicPattern,
      enabled: overrideDraft.enabled,
      failMode: overrideDraft.failMode,
      scanners: uniqueStrings(overrideDraft.scanners),
    };

    setForm((curr) => {
      const next = [...curr.topicOverrides];
      if (editingOverrideIndex !== null && next[editingOverrideIndex]) {
        next[editingOverrideIndex] = normalized;
      } else {
        next.push(normalized);
      }
      return { ...curr, topicOverrides: next };
    });
    resetOverrideEditor();
  };

  const removeOverride = (index: number) => {
    setForm((curr) => ({
      ...curr,
      topicOverrides: curr.topicOverrides.filter((_, idx) => idx !== index),
    }));
  };

  const toggleDraftScanner = (scanner: string) => {
    setOverrideDraft((curr) => {
      const exists = curr.scanners.includes(scanner);
      const scanners = exists
        ? curr.scanners.filter((entry) => entry !== scanner)
        : [...curr.scanners, scanner];
      return { ...curr, scanners: uniqueStrings(scanners) };
    });
  };

  const onSaveOverrides = () => {
    saveOverridesMutation.mutate(form.topicOverrides);
  };

  if (configError) {
    return <ErrorBanner message={configErr instanceof Error ? configErr.message : "Failed to load output safety settings"} onRetry={() => void refetchConfig()} />;
  }

  if (configLoading) {
    return (
      <div className="space-y-4">
        {Array.from({ length: 3 }, (_, i) => (
          <div key={i} className="h-32 animate-pulse rounded-2xl bg-surface2" />
        ))}
      </div>
    );
  }

  return (
    <div className="space-y-6 pb-12">
      <Card>
        <CardHeader className="flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <CardTitle>Output Safety Scanning</CardTitle>
            <CardDescription>
              Configure post-execution output checks and default failure behavior.
            </CardDescription>
          </div>
          <Badge variant={effectiveEnabled ? "success" : "warning"}>
            {effectiveEnabled ? "Enabled" : "Disabled"}
          </Badge>
        </CardHeader>

        <div className="space-y-4">
          <div className="flex flex-wrap items-center gap-3 rounded-2xl border border-border p-4">
            <button
              type="button"
              role="switch"
              aria-checked={form.enabled}
              aria-label="Enable or disable output safety scanning"
              title="When enabled, Cordum scans completed job output for secrets, PII, and injection patterns before release."
              onClick={() => setForm((curr) => ({ ...curr, enabled: !curr.enabled }))}
              className={`relative inline-flex h-6 w-11 shrink-0 rounded-full border-2 border-transparent transition-colors ${
                form.enabled ? "bg-accent" : "bg-surface2"
              }`}
            >
              <span
                className={`pointer-events-none inline-block h-5 w-5 rounded-full bg-card shadow-sm transition-transform ${
                  form.enabled ? "translate-x-5" : "translate-x-0"
                }`}
              />
            </button>
            <div>
              <p className="text-sm font-semibold text-ink">Enable Output Safety Scanning</p>
              <p className="text-xs text-muted-foreground">Controls whether result payloads are checked before release.</p>
            </div>
            <div className="ml-auto flex items-center gap-2">
              {form.enabled ? (
                <ShieldCheck className="h-4 w-4 text-success" />
              ) : (
                <ShieldOff className="h-4 w-4 text-warning" />
              )}
              <span className="text-xs font-medium text-muted-foreground">
                {form.enabled ? "Scanning active" : "Scanning off"}
              </span>
            </div>
          </div>

          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            <div className="space-y-1">
              <label className="text-xs font-semibold text-muted-foreground">Fail Mode</label>
              <Select
                value={form.failMode}
                onChange={(e) =>
                  setForm((curr) => ({
                    ...curr,
                    failMode: e.target.value === "closed" ? "closed" : "open",
                  }))
                }
              >
                <option value="open">Open (allow result when scanner fails)</option>
                <option value="closed">Closed (deny result when scanner fails)</option>
              </Select>
            </div>

            <div className="space-y-1">
              <label className="text-xs font-semibold text-muted-foreground">Default Action on Scan Failure</label>
              <Select
                value={form.failureAction}
                onChange={(e) =>
                  setForm((curr) => ({
                    ...curr,
                    failureAction: e.target.value === "deny" ? "deny" : "allow",
                  }))
                }
              >
                <option value="allow">Allow</option>
                <option value="deny">Deny</option>
              </Select>
            </div>

            <div className="space-y-1">
              <label className="text-xs font-semibold text-muted-foreground">Scan Timeout (ms)</label>
              <Input
                type="number"
                min={100}
                step={100}
                value={String(form.scanTimeoutMs)}
                onChange={(e) =>
                  setForm((curr) => ({
                    ...curr,
                    scanTimeoutMs: Math.max(100, Number.parseInt(e.target.value || "0", 10) || 100),
                  }))
                }
              />
            </div>

            <div className="space-y-1">
              <label className="text-xs font-semibold text-muted-foreground">Max Payload Size (KB)</label>
              <Input
                type="number"
                min={32}
                step={32}
                value={String(form.maxPayloadKb)}
                onChange={(e) =>
                  setForm((curr) => ({
                    ...curr,
                    maxPayloadKb: Math.max(32, Number.parseInt(e.target.value || "0", 10) || 32),
                  }))
                }
              />
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-2 text-xs">
            <Badge variant={kernelConnected ? "success" : kernelConnected === false ? "danger" : "default"}>
              Kernel {kernelConnected ? "connected" : kernelConnected === false ? "disconnected" : "unknown"}
            </Badge>
            <Badge variant="info">Last check: {formatLastCheck(lastCheck)}</Badge>
            <Badge variant={outputStatus?.fail_mode === "closed" ? "warning" : "default"}>
              Runtime fail mode: {outputStatus?.fail_mode || form.failMode}
            </Badge>
          </div>

          <div className="flex items-center gap-3">
            <Button
              type="button"
              onClick={onSave}
              disabled={!isDirty || saveMutation.isPending}
            >
              {saveMutation.isPending ? "Saving..." : "Save Output Safety Settings"}
            </Button>
            {isDirty ? (
              <button
                type="button"
                className="text-xs font-semibold text-muted-foreground hover:text-ink"
                onClick={() => setForm(baseline)}
              >
                Reset
              </button>
            ) : (
              <span className="text-xs text-muted-foreground">No unsaved changes</span>
            )}
          </div>
        </div>
      </Card>

      <Card>
        <CardHeader className="flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <CardTitle>Per-Topic Overrides</CardTitle>
            <CardDescription>
              Topic-specific rules take precedence over global Output Safety defaults.
            </CardDescription>
          </div>
          <div className="flex items-center gap-2">
            <Button variant="outline" type="button" onClick={startAddOverride}>
              Add Override
            </Button>
            <Button
              type="button"
              onClick={onSaveOverrides}
              disabled={!overridesDirty || saveOverridesMutation.isPending}
            >
              {saveOverridesMutation.isPending ? "Saving..." : "Save Overrides"}
            </Button>
          </div>
        </CardHeader>

        {overrideFormOpen && (
          <div className="mb-4 rounded-2xl border border-border bg-surface px-4 py-4">
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
              <div className="space-y-1">
                <label className="text-xs font-semibold text-muted-foreground">Topic Pattern</label>
                <Input
                  type="text"
                  placeholder="job.reports.*"
                  value={overrideDraft.topicPattern}
                  onChange={(e) =>
                    setOverrideDraft((curr) => ({ ...curr, topicPattern: e.target.value }))
                  }
                />
              </div>
              <div className="space-y-1">
                <label className="text-xs font-semibold text-muted-foreground">Fail Mode</label>
                <Select
                  value={overrideDraft.failMode}
                  onChange={(e) =>
                    setOverrideDraft((curr) => ({
                      ...curr,
                      failMode: e.target.value === "closed" ? "closed" : "open",
                    }))
                  }
                >
                  <option value="open">Open</option>
                  <option value="closed">Closed</option>
                </Select>
              </div>
            </div>

            <div className="mt-3 rounded-xl border border-border bg-card/60 p-3">
              <div className="mb-2 flex items-center justify-between">
                <p className="text-xs font-semibold text-muted-foreground">Scanners (multi-select)</p>
                <label className="inline-flex items-center gap-2 text-xs font-medium text-muted-foreground">
                  <input
                    type="checkbox"
                    checked={overrideDraft.enabled}
                    onChange={(e) =>
                      setOverrideDraft((curr) => ({ ...curr, enabled: e.target.checked }))
                    }
                  />
                  Enabled
                </label>
              </div>

              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                {SCANNER_OPTIONS.map((scanner) => (
                  <label
                    key={scanner.value}
                    className="flex items-start gap-2 rounded-2xl border border-border px-3 py-2 text-xs"
                  >
                    <input
                      type="checkbox"
                      checked={overrideDraft.scanners.includes(scanner.value)}
                      onChange={() => toggleDraftScanner(scanner.value)}
                      className="mt-0.5"
                    />
                    <span>
                      <span className="block font-semibold text-ink">{scanner.label}</span>
                      <span className="block text-muted-foreground">{scanner.description}</span>
                    </span>
                  </label>
                ))}
              </div>
            </div>

            <div className="mt-3 flex items-center gap-2">
              <Button type="button" onClick={upsertOverride}>
                {editingOverrideIndex !== null ? "Update Override" : "Add Override"}
              </Button>
              <Button variant="ghost" type="button" onClick={resetOverrideEditor}>
                Cancel
              </Button>
            </div>
          </div>
        )}

        {form.topicOverrides.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No topic overrides configured. Use <span className="font-semibold">Add Override</span> to
            define exceptions.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[720px] text-left text-sm">
              <thead className="text-xs uppercase tracking-wide text-muted-foreground">
                <tr>
                  <th className="pb-2 font-semibold">Topic Pattern</th>
                  <th className="pb-2 font-semibold">Enabled</th>
                  <th className="pb-2 font-semibold">Fail Mode</th>
                  <th className="pb-2 font-semibold">Scanners</th>
                  <th className="pb-2 font-semibold">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {form.topicOverrides.map((override, index) => (
                  <tr key={`${override.topicPattern}-${index}`}>
                    <td className="py-2 font-mono text-xs text-ink">{override.topicPattern}</td>
                    <td className="py-2">
                      <Badge variant={override.enabled ? "success" : "default"}>
                        {override.enabled ? "Yes" : "No"}
                      </Badge>
                    </td>
                    <td className="py-2 text-ink">{override.failMode}</td>
                    <td className="py-2 text-muted-foreground">{override.scanners.join(", ") || "none"}</td>
                    <td className="py-2">
                      <div className="flex items-center gap-2">
                        <button
                          type="button"
                          className="text-xs font-semibold text-accent hover:underline"
                          onClick={() => startEditOverride(index)}
                        >
                          Edit
                        </button>
                        <button
                          type="button"
                          className="text-xs font-semibold text-danger hover:underline"
                          onClick={() => removeOverride(index)}
                        >
                          Remove
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        <MetricCard
          title="Checks (24h)"
          value={totalChecks24h}
          detail="Output policy evaluations"
          icon={<Activity className="h-4 w-4 text-muted-foreground" />}
        />
        <MetricCard
          title="Quarantined (24h)"
          value={quarantined24h}
          detail="Denied or quarantined results"
          icon={<AlertTriangle className="h-4 w-4 text-warning" />}
        />
        <MetricCard
          title="Avg Latency"
          value={`${avgLatencyMs.toFixed(1)}ms`}
          detail="Scanner processing time"
          icon={<Activity className="h-4 w-4 text-muted-foreground" />}
        />
      </div>

      <Card>
        <CardHeader className="flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <CardTitle>Output Rules</CardTitle>
            <CardDescription>Summary of active output-focused policy rules.</CardDescription>
          </div>
          <Link to="/govern/overview?tab=input-rules" className="text-xs font-semibold text-accent hover:underline">
            Open Policy Studio
          </Link>
        </CardHeader>
        <div className="flex items-center gap-2 text-sm">
          <Badge variant={outputRuleSummary.enabled > 0 ? "success" : "default"}>
            {outputRuleSummary.enabled} enabled
          </Badge>
          <span className="text-muted-foreground">of {outputRuleSummary.total} output rules</span>
        </div>
      </Card>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>Recent Denials</CardTitle>
            <CardDescription>Latest output-policy denials from audit events.</CardDescription>
          </div>
        </CardHeader>
        {recentDenials.length === 0 ? (
          <p className="text-sm text-muted-foreground">No output-policy denials in recent audit entries.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[580px] text-left text-sm">
              <thead className="text-xs uppercase tracking-wide text-muted-foreground">
                <tr>
                  <th className="pb-2 font-semibold">Time</th>
                  <th className="pb-2 font-semibold">Action</th>
                  <th className="pb-2 font-semibold">Actor</th>
                  <th className="pb-2 font-semibold">Bundle</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {recentDenials.map((entry) => (
                  <tr key={entry.id}>
                    <td className="py-2 text-muted-foreground">{formatLastCheck(entry.timestamp)}</td>
                    <td className="py-2">
                      <Badge variant="danger">{entry.action || "deny"}</Badge>
                    </td>
                    <td className="py-2 text-ink">{entry.actor || "system"}</td>
                    <td className="py-2 text-muted-foreground">{entry.bundleId || "n/a"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}
