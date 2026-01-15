import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { Area, AreaChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { api } from "../lib/api";
import { formatDateTime, formatPercent, formatRelative, formatShortDate } from "../lib/format";
import { getDLQGuidance, getGuidanceSeverityBg } from "../lib/dlq-guidance";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Badge } from "../components/ui/Badge";
import { Select } from "../components/ui/Select";
import { Textarea } from "../components/ui/Textarea";
import { Input } from "../components/ui/Input";
import { ProgressBar } from "../components/ProgressBar";
import { useConfigStore } from "../state/config";
import type { DLQEntry, Heartbeat, LicenseInfo } from "../types/api";

type DiffLine = {
  left: string;
  right: string;
  match: boolean;
};

type DlqTrendPoint = {
  time: string;
  count: number;
  backlog: number;
};

const STALE_WORKER_MINUTES = 2;
const TAB_OPTIONS = ["health", "workers", "dlq", "config", "observability", "alerting"] as const;
type SystemTab = (typeof TAB_OPTIONS)[number];

function resolveTab(value: string | null): SystemTab {
  if (!value) {
    return "health";
  }
  return TAB_OPTIONS.includes(value as SystemTab) ? (value as SystemTab) : "health";
}

const SEVERITY_OPTIONS = ["info", "warning", "error", "critical"] as const;
type AlertSeverity = (typeof SEVERITY_OPTIONS)[number];

function asRecord(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  return value as Record<string, unknown>;
}

function asString(value: unknown, fallback = ""): string {
  return typeof value === "string" ? value : fallback;
}

function asBool(value: unknown, fallback = false): boolean {
  return typeof value === "boolean" ? value : fallback;
}

function asSeverity(value: unknown, fallback: AlertSeverity): AlertSeverity {
  const candidate = asString(value, fallback);
  return SEVERITY_OPTIONS.includes(candidate as AlertSeverity) ? (candidate as AlertSeverity) : fallback;
}

function parseJsonObject(value: string, label: string): { parsed: Record<string, unknown>; error?: string } {
  const trimmed = value.trim();
  if (!trimmed) {
    return { parsed: {} };
  }
  try {
    const parsed = JSON.parse(trimmed);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return { parsed: {}, error: `${label} must be a JSON object.` };
    }
    return { parsed: parsed as Record<string, unknown> };
  } catch {
    return { parsed: {}, error: `${label} must be valid JSON.` };
  }
}

function resolveGrafanaUrl(baseUrl: string, path: string): string {
  if (!path) {
    return "";
  }
  const trimmed = path.trim();
  if (trimmed.startsWith("http://") || trimmed.startsWith("https://")) {
    return trimmed;
  }
  if (!baseUrl) {
    return "";
  }
  const base = baseUrl.replace(/\/$/, "");
  const suffix = trimmed.replace(/^\//, "");
  return `${base}/${suffix}`;
}

function buildLineDiff(left: string, right: string): DiffLine[] {
  const leftLines = left.split("\n");
  const rightLines = right.split("\n");
  const max = Math.max(leftLines.length, rightLines.length);
  const out: DiffLine[] = [];
  for (let i = 0; i < max; i += 1) {
    const l = leftLines[i] ?? "";
    const r = rightLines[i] ?? "";
    out.push({ left: l, right: r, match: l === r });
  }
  return out;
}

function buildDlqTrend(entries: DLQEntry[]): DlqTrendPoint[] {
  const buckets = new Map<string, number>();
  entries.forEach((entry) => {
    const date = new Date(entry.created_at);
    if (Number.isNaN(date.getTime())) {
      return;
    }
    const key = new Date(date.getFullYear(), date.getMonth(), date.getDate(), date.getHours()).toISOString();
    buckets.set(key, (buckets.get(key) || 0) + 1);
  });
  const points = Array.from(buckets.entries())
    .map(([time, count]) => ({ time, count, backlog: 0 }))
    .sort((a, b) => a.time.localeCompare(b.time));
  let running = 0;
  return points.map((point) => {
    running += point.count;
    return { ...point, backlog: running };
  });
}

export function SystemPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const tabParam = searchParams.get("tab");
  const activeTab = resolveTab(tabParam);
  const queryClient = useQueryClient();
  const principalRole = useConfigStore((state) => state.principalRole);
  const canEditConfig = principalRole === "admin";
  const statusQuery = useQuery({ queryKey: ["status"], queryFn: () => api.getStatus() });
  const workersQuery = useQuery({ queryKey: ["workers"], queryFn: () => api.listWorkers() });
  const dlqQuery = useInfiniteQuery({
    queryKey: ["dlq"],
    queryFn: ({ pageParam }) => api.listDLQPage(100, pageParam as number | undefined),
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    initialPageParam: undefined as number | undefined,
  });
  const schemasQuery = useQuery({ queryKey: ["schemas"], queryFn: () => api.listSchemas() });
  const systemConfigQuery = useQuery({
    queryKey: ["config", "system", "default"],
    queryFn: () => api.getConfig("system", "default"),
  });

  const [configScope, setConfigScope] = useState("system");
  const [configScopeId, setConfigScopeId] = useState("default");
  const [otelEnabled, setOtelEnabled] = useState(false);
  const [otelEndpoint, setOtelEndpoint] = useState("");
  const [otelProtocol, setOtelProtocol] = useState("grpc");
  const [otelHeadersText, setOtelHeadersText] = useState("{}");
  const [otelResourceAttrsText, setOtelResourceAttrsText] = useState("{}");
  const [grafanaBaseUrl, setGrafanaBaseUrl] = useState("");
  const [grafanaSystemDashboard, setGrafanaSystemDashboard] = useState("");
  const [grafanaWorkflowDashboard, setGrafanaWorkflowDashboard] = useState("");
  const [pagerDutyEnabled, setPagerDutyEnabled] = useState(false);
  const [pagerDutyKey, setPagerDutyKey] = useState("");
  const [pagerDutySeverity, setPagerDutySeverity] = useState<AlertSeverity>("critical");
  const [slackEnabled, setSlackEnabled] = useState(false);
  const [slackWebhook, setSlackWebhook] = useState("");
  const [slackSeverity, setSlackSeverity] = useState<AlertSeverity>("error");
  const [observabilityError, setObservabilityError] = useState<string | null>(null);
  const [alertingError, setAlertingError] = useState<string | null>(null);
  const setTab = (next: SystemTab) => {
    const updated = new URLSearchParams(searchParams);
    if (next === "health") {
      updated.delete("tab");
    } else {
      updated.set("tab", next);
    }
    setSearchParams(updated, { replace: true });
  };

  const configQuery = useQuery({
    queryKey: ["config", configScope, configScopeId],
    queryFn: () => api.getConfig(configScope, configScopeId),
  });

  const systemConfigData = useMemo(() => asRecord(systemConfigQuery.data?.data), [systemConfigQuery.data]);

  const saveConfigMutation = useMutation({
    mutationFn: (payload: { scopeId: string; data: Record<string, unknown>; meta?: Record<string, string> }) =>
      api.setConfig("system", payload.scopeId, payload.data, payload.meta),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["config"] }),
  });

  useEffect(() => {
    if (tabParam && !TAB_OPTIONS.includes(tabParam as SystemTab)) {
      const updated = new URLSearchParams(searchParams);
      updated.delete("tab");
      setSearchParams(updated, { replace: true });
    }
  }, [tabParam, searchParams, setSearchParams]);

  useEffect(() => {
    if (!systemConfigQuery.data) {
      return;
    }
    const observability = asRecord(systemConfigData.observability);
    const otel = asRecord(observability.otel);
    const grafana = asRecord(observability.grafana);
    const dashboards = asRecord(grafana.dashboards);
    const alerting = asRecord(systemConfigData.alerting);
    const pagerDuty = asRecord(alerting.pagerduty);
    const slack = asRecord(alerting.slack);

    setOtelEnabled(asBool(otel.enabled));
    setOtelEndpoint(asString(otel.endpoint));
    setOtelProtocol(asString(otel.protocol, "grpc"));
    setOtelHeadersText(JSON.stringify(asRecord(otel.headers), null, 2));
    setOtelResourceAttrsText(JSON.stringify(asRecord(otel.resource_attributes), null, 2));
    setGrafanaBaseUrl(asString(grafana.base_url));
    setGrafanaSystemDashboard(asString(dashboards.system_overview));
    setGrafanaWorkflowDashboard(asString(dashboards.workflow_performance));

    setPagerDutyEnabled(asBool(pagerDuty.enabled));
    setPagerDutyKey(asString(pagerDuty.integration_key));
    setPagerDutySeverity(asSeverity(pagerDuty.severity, "critical"));
    setSlackEnabled(asBool(slack.enabled));
    setSlackWebhook(asString(slack.webhook_url));
    setSlackSeverity(asSeverity(slack.severity, "error"));
    setObservabilityError(null);
    setAlertingError(null);
  }, [systemConfigQuery.data, systemConfigData]);

  const retryMutation = useMutation({
    mutationFn: (jobId: string) => api.retryDLQ(jobId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["dlq"] }),
  });

  const deleteMutation = useMutation({
    mutationFn: (jobId: string) => api.deleteDLQ(jobId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["dlq"] }),
  });

  const status = statusQuery.data as Record<string, unknown> | undefined;
  const nats = status?.nats as Record<string, unknown> | undefined;
  const redis = status?.redis as Record<string, unknown> | undefined;
  const workersCount = (status?.workers as Record<string, unknown> | undefined)?.count as number | undefined;
  const license = status?.license as LicenseInfo | undefined;
  const licenseMode = (license?.mode || "community").toLowerCase();
  const licenseLabel = licenseMode === "enterprise" ? "Enterprise" : "Community";
  const licenseStatus = license?.status || (licenseMode === "enterprise" ? "unknown" : "active");
  const licensePlan = license?.plan || licenseLabel;

  const workers = useMemo(() => (workersQuery.data || []) as Heartbeat[], [workersQuery.data]);
  const dlqEntries = useMemo(
    () => (dlqQuery.data?.pages.flatMap((page) => page.items) || []) as DLQEntry[],
    [dlqQuery.data]
  );
  const dlqTrend = useMemo(() => buildDlqTrend(dlqEntries), [dlqEntries]);
  const staleWorkers = useMemo(() => {
    const cutoff = Date.now() - STALE_WORKER_MINUTES * 60 * 1000;
    return workers.filter((worker) => {
      if (!worker.updated_at) {
        return false;
      }
      const ts = new Date(worker.updated_at).getTime();
      return Number.isFinite(ts) && ts < cutoff;
    });
  }, [workers]);

  const poolMetrics = useMemo(() => {
    const poolsConfig = (systemConfigQuery.data?.data?.pools || {}) as Record<string, unknown>;
    const poolDefs = (poolsConfig as { pools?: Record<string, { requires?: string[] }> }).pools || {};
    const topics = (poolsConfig as { topics?: Record<string, string | string[]> }).topics || {};
    const topicCounts: Record<string, number> = {};
    Object.entries(topics).forEach(([_, value]) => {
      if (Array.isArray(value)) {
        value.forEach((pool) => {
          topicCounts[pool] = (topicCounts[pool] || 0) + 1;
        });
      } else if (typeof value === "string") {
        topicCounts[value] = (topicCounts[value] || 0) + 1;
      }
    });
    const pools = new Set<string>([...Object.keys(poolDefs), ...workers.map((worker) => worker.pool || "default")]);
    return Array.from(pools)
      .map((pool) => {
        const poolWorkers = workers.filter((worker) => (worker.pool || "default") === pool);
        const cpuValues = poolWorkers.map((worker) => worker.cpu_load).filter((v): v is number => typeof v === "number");
        const memValues = poolWorkers.map((worker) => worker.memory_load).filter((v): v is number => typeof v === "number");
        const avgCpu = cpuValues.length ? cpuValues.reduce((sum, v) => sum + v, 0) / cpuValues.length : 0;
        const avgMem = memValues.length ? memValues.reduce((sum, v) => sum + v, 0) / memValues.length : 0;
        return {
          name: pool,
          workers: poolWorkers.length,
          topics: topicCounts[pool] || 0,
          requires: poolDefs[pool]?.requires || [],
          avgCpu,
          avgMem,
        };
      })
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [systemConfigQuery.data, workers]);

  const hotPools = useMemo(
    () => poolMetrics.filter((pool) => pool.avgCpu >= 80 || pool.avgMem >= 80),
    [poolMetrics]
  );

  const otelStatusLabel = otelEnabled ? (otelEndpoint ? "configured" : "missing endpoint") : "disabled";
  const otelStatusVariant = otelEnabled ? (otelEndpoint ? "success" : "warning") : "default";
  const grafanaSystemUrl = resolveGrafanaUrl(grafanaBaseUrl, grafanaSystemDashboard);
  const grafanaWorkflowUrl = resolveGrafanaUrl(grafanaBaseUrl, grafanaWorkflowDashboard);
  const grafanaConfigured = Boolean(grafanaBaseUrl || grafanaSystemDashboard || grafanaWorkflowDashboard);
  const grafanaStatusVariant = grafanaConfigured ? "info" : "default";
  const pagerDutyStatusLabel = pagerDutyEnabled ? (pagerDutyKey ? "active" : "needs key") : "disabled";
  const pagerDutyStatusVariant = pagerDutyEnabled ? (pagerDutyKey ? "success" : "warning") : "default";
  const slackStatusLabel = slackEnabled ? (slackWebhook ? "active" : "needs webhook") : "disabled";
  const slackStatusVariant = slackEnabled ? (slackWebhook ? "success" : "warning") : "default";

  const handleSaveObservability = () => {
    setObservabilityError(null);
    const headersResult = parseJsonObject(otelHeadersText, "Headers");
    if (headersResult.error) {
      setObservabilityError(headersResult.error);
      return;
    }
    const attrsResult = parseJsonObject(otelResourceAttrsText, "Resource attributes");
    if (attrsResult.error) {
      setObservabilityError(attrsResult.error);
      return;
    }
    const currentObservability = asRecord(systemConfigData.observability);
    const currentGrafana = asRecord(currentObservability.grafana);
    const updatedData: Record<string, unknown> = {
      ...systemConfigData,
      observability: {
        ...currentObservability,
        otel: {
          enabled: otelEnabled,
          endpoint: otelEndpoint.trim(),
          protocol: otelProtocol === "http" ? "http" : "grpc",
          headers: headersResult.parsed,
          resource_attributes: attrsResult.parsed,
        },
        grafana: {
          ...currentGrafana,
          base_url: grafanaBaseUrl.trim(),
          dashboards: {
            system_overview: grafanaSystemDashboard.trim(),
            workflow_performance: grafanaWorkflowDashboard.trim(),
          },
        },
      },
    };
    saveConfigMutation.mutate(
      { scopeId: systemConfigQuery.data?.scope_id || "default", data: updatedData, meta: { source: "dashboard", section: "observability" } },
      {
        onError: (err) => {
          setObservabilityError(err instanceof Error ? err.message : "Failed to save observability config.");
        },
      }
    );
  };

  const handleSaveAlerting = () => {
    setAlertingError(null);
    if (pagerDutyEnabled && !pagerDutyKey.trim()) {
      setAlertingError("PagerDuty integration key is required when enabled.");
      return;
    }
    if (slackEnabled && !slackWebhook.trim()) {
      setAlertingError("Slack webhook URL is required when enabled.");
      return;
    }
    const currentAlerting = asRecord(systemConfigData.alerting);
    const updatedData: Record<string, unknown> = {
      ...systemConfigData,
      alerting: {
        ...currentAlerting,
        pagerduty: {
          enabled: pagerDutyEnabled,
          integration_key: pagerDutyKey.trim(),
          severity: pagerDutySeverity,
        },
        slack: {
          enabled: slackEnabled,
          webhook_url: slackWebhook.trim(),
          severity: slackSeverity,
        },
      },
    };
    saveConfigMutation.mutate(
      { scopeId: systemConfigQuery.data?.scope_id || "default", data: updatedData, meta: { source: "dashboard", section: "alerting" } },
      {
        onError: (err) => {
          setAlertingError(err instanceof Error ? err.message : "Failed to save alerting config.");
        },
      }
    );
  };

  const baseConfigText = useMemo(() => JSON.stringify(systemConfigQuery.data?.data || {}, null, 2), [systemConfigQuery.data]);
  const currentConfigText = useMemo(() => JSON.stringify(configQuery.data?.data || {}, null, 2), [configQuery.data]);
  const configDiff = useMemo(() => buildLineDiff(baseConfigText, currentConfigText), [baseConfigText, currentConfigText]);

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>System Management</CardTitle>
          <div className="flex flex-wrap gap-2">
            <Button variant={activeTab === "health" ? "primary" : "outline"} size="sm" onClick={() => setTab("health")}>Health</Button>
            <Button variant={activeTab === "workers" ? "primary" : "outline"} size="sm" onClick={() => setTab("workers")}>Workers</Button>
            <Button variant={activeTab === "dlq" ? "primary" : "outline"} size="sm" onClick={() => setTab("dlq")}>DLQ</Button>
            <Button variant={activeTab === "config" ? "primary" : "outline"} size="sm" onClick={() => setTab("config")}>Config</Button>
            <Button variant={activeTab === "observability" ? "primary" : "outline"} size="sm" onClick={() => setTab("observability")}>Observability</Button>
            <Button variant={activeTab === "alerting" ? "primary" : "outline"} size="sm" onClick={() => setTab("alerting")}>Alerting</Button>
          </div>
        </CardHeader>
      </Card>

      {activeTab === "health" && (
        <>
          <Card>
            <CardHeader>
              <CardTitle>System Health</CardTitle>
              <div className="text-xs text-muted">Gateway status snapshot</div>
            </CardHeader>
            <div className="grid gap-4 lg:grid-cols-4">
              <div className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="text-xs uppercase tracking-[0.2em] text-muted">NATS</div>
                <div className="mt-2 text-sm font-semibold text-ink">{String(nats?.status || "unknown")}</div>
                <div className="text-xs text-muted">{String(nats?.url || "-")}</div>
              </div>
              <div className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="text-xs uppercase tracking-[0.2em] text-muted">Redis</div>
                <div className="mt-2 text-sm font-semibold text-ink">{redis?.ok ? "ok" : "unavailable"}</div>
                <div className="text-xs text-muted">{String(redis?.error || "-")}</div>
              </div>
              <div className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="text-xs uppercase tracking-[0.2em] text-muted">Workers</div>
                <div className="mt-2 text-sm font-semibold text-ink">{workersCount ?? workers.length}</div>
                <div className="text-xs text-muted">Active worker heartbeats</div>
              </div>
              <div className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="flex items-center justify-between text-xs uppercase tracking-[0.2em] text-muted">
                  <span>License</span>
                  <Badge variant={licenseMode === "enterprise" ? "info" : "default"}>{licenseLabel}</Badge>
                </div>
                <div className="mt-2 text-sm font-semibold text-ink">{licensePlan}</div>
                <div className="text-xs text-muted">
                  {licenseMode === "enterprise"
                    ? `Status: ${licenseStatus}`
                    : "Self-hosted community license"}
                </div>
                {licenseMode === "enterprise" && (license?.expires_at || license?.org_id) ? (
                  <div className="text-xs text-muted">
                    {license?.org_id ? `Org: ${license.org_id}` : "Org: -"}{" "}
                    {license?.expires_at ? `• Expires ${formatDateTime(license.expires_at)}` : ""}
                  </div>
                ) : null}
              </div>
            </div>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Attention Summary</CardTitle>
              <div className="text-xs text-muted">What needs triage right now</div>
            </CardHeader>
            <div className="grid gap-3 lg:grid-cols-3">
              <div className="list-row border-l-4 border-warning">
                <div className="flex items-center justify-between">
                  <div>
                    <div className="text-xs uppercase tracking-[0.2em] text-muted">Stale workers</div>
                    <div className="text-lg font-semibold text-ink">{staleWorkers.length}</div>
                  </div>
                  <Button variant="outline" size="sm" type="button" onClick={() => setTab("workers")}>
                    View
                  </Button>
                </div>
                <div className="mt-2 text-xs text-muted">
                  Last heartbeat &gt; {STALE_WORKER_MINUTES}m
                </div>
              </div>

              <div className="list-row border-l-4 border-danger">
                <div className="flex items-center justify-between">
                  <div>
                    <div className="text-xs uppercase tracking-[0.2em] text-muted">DLQ backlog</div>
                    <div className="text-lg font-semibold text-ink">{dlqEntries.length}</div>
                  </div>
                  <Button variant="outline" size="sm" type="button" onClick={() => setTab("dlq")}>
                    Review
                  </Button>
                </div>
                <div className="mt-2 text-xs text-muted">Retry or purge stuck jobs</div>
              </div>

              <div className="list-row border-l-4 border-danger">
                <div className="flex items-center justify-between">
                  <div>
                    <div className="text-xs uppercase tracking-[0.2em] text-muted">Hot pools</div>
                    <div className="text-lg font-semibold text-ink">{hotPools.length}</div>
                  </div>
                  <Button variant="outline" size="sm" type="button" onClick={() => setTab("health")}>
                    Inspect
                  </Button>
                </div>
                <div className="mt-2 text-xs text-muted">Pools above 80% CPU or memory</div>
              </div>
            </div>
          </Card>

          <Card id="pools">
            <CardHeader>
              <CardTitle>Pool Saturation</CardTitle>
              <div className="text-xs text-muted">Live worker utilization by pool</div>
            </CardHeader>
            {poolMetrics.length === 0 ? (
              <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
                No pool metrics available.
              </div>
            ) : (
              <div className="grid gap-4 lg:grid-cols-2">
                {poolMetrics.map((pool) => (
                  <div key={pool.name} className="rounded-2xl border border-border bg-white/70 p-4">
                    <div className="flex items-center justify-between">
                      <div>
                        <div className="text-sm font-semibold text-ink">{pool.name}</div>
                        <div className="text-xs text-muted">{pool.topics} topics · {pool.workers} workers</div>
                      </div>
                      <Badge variant="info">{pool.requires.length ? pool.requires.join(", ") : "general"}</Badge>
                    </div>
                    <div className="mt-3">
                      <div className="mb-1 flex items-center justify-between text-xs text-muted">
                        <span>CPU load</span>
                        <span>{formatPercent(pool.avgCpu)}</span>
                      </div>
                      <ProgressBar value={pool.avgCpu} />
                    </div>
                    <div className="mt-3">
                      <div className="mb-1 flex items-center justify-between text-xs text-muted">
                        <span>Memory load</span>
                        <span>{formatPercent(pool.avgMem)}</span>
                      </div>
                      <ProgressBar value={pool.avgMem} />
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Card>
        </>
      )}

      {activeTab === "workers" && (
        <Card id="workers">
          <CardHeader>
            <CardTitle>Workers</CardTitle>
          </CardHeader>
          {workers.length === 0 ? (
            <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">No active workers.</div>
          ) : (
            <div className="grid gap-3 lg:grid-cols-2">
              {workers.map((worker, index) => (
                <div key={`${worker.worker_id}-${index}`} className="rounded-2xl border border-border bg-white/70 p-4">
                  <div className="flex items-center justify-between">
                    <div className="text-sm font-semibold text-ink">{worker.worker_id || "worker"}</div>
                    <Badge variant="info">{worker.pool || "default"}</Badge>
                  </div>
                  <div className="mt-2 text-xs text-muted">CPU {worker.cpu_load ?? "-"}%</div>
                  <div className="text-xs text-muted">Memory {worker.memory_load ?? "-"}%</div>
                </div>
              ))}
            </div>
          )}
        </Card>
      )}

      {activeTab === "dlq" && (
        <>
          <Card id="dlq">
            <CardHeader>
              <CardTitle>DLQ Burn-down</CardTitle>
              <div className="text-xs text-muted">Backlog growth over time</div>
            </CardHeader>
            {dlqTrend.length === 0 ? (
              <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">No DLQ data.</div>
            ) : (
              <div className="h-56">
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={dlqTrend} margin={{ left: 0, right: 0, top: 10, bottom: 0 }}>
                    <defs>
                      <linearGradient id="colorDlq" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="5%" stopColor="#b83a3a" stopOpacity={0.55} />
                        <stop offset="95%" stopColor="#b83a3a" stopOpacity={0.05} />
                      </linearGradient>
                    </defs>
                    <XAxis
                      dataKey="time"
                      tickFormatter={(value) => formatShortDate(value)}
                      tick={{ fontSize: 10 }}
                      axisLine={false}
                      tickLine={false}
                    />
                    <YAxis hide />
                    <Tooltip
                      labelFormatter={(value) => formatShortDate(value as string)}
                      formatter={(value: number) => [value, "backlog"]}
                    />
                    <Area type="monotone" dataKey="backlog" stroke="#b83a3a" fill="url(#colorDlq)" />
                  </AreaChart>
                </ResponsiveContainer>
              </div>
            )}
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>DLQ Management</CardTitle>
              <div className="text-xs text-muted">Retry or purge failed jobs</div>
            </CardHeader>
            {dlqEntries.length === 0 ? (
              <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">DLQ is empty.</div>
            ) : (
              <div className="space-y-3">
                {dlqEntries.map((entry) => {
                  const guidance = getDLQGuidance(entry);
                  return (
                    <div key={entry.job_id} className="list-row">
                      <div className="grid gap-3 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)_auto] lg:items-center">
                        <div>
                          <div className="text-sm font-semibold text-ink">
                            <Link to={`/jobs/${entry.job_id}`} className="hover:underline">
                              Job {entry.job_id.slice(0, 8)}
                            </Link>
                            {entry.topic ? (
                              <span className="ml-2 text-xs font-normal text-muted">
                                {entry.topic}
                              </span>
                            ) : null}
                          </div>
                          <div className="text-xs text-muted">
                            {entry.reason_code ? (
                              <span className="font-mono text-warning">{entry.reason_code}</span>
                            ) : null}
                            {entry.reason_code && entry.reason ? " · " : ""}
                            {entry.reason || entry.status}
                          </div>
                          {entry.attempts ? (
                            <div className="text-xs text-muted">
                              Attempts: {entry.attempts}
                            </div>
                          ) : null}
                        </div>
                        <div className="text-xs text-muted">Created {formatRelative(entry.created_at)}</div>
                        <div className="flex flex-wrap gap-2 justify-end">
                          <Button
                            variant="outline"
                            size="sm"
                            type="button"
                            onClick={() => retryMutation.mutate(entry.job_id)}
                            disabled={retryMutation.isPending}
                          >
                            Retry
                          </Button>
                          <Button
                            variant="danger"
                            size="sm"
                            type="button"
                            onClick={() => deleteMutation.mutate(entry.job_id)}
                            disabled={deleteMutation.isPending}
                          >
                            Delete
                          </Button>
                        </div>
                      </div>
                      {guidance ? (
                        <div
                          className={`mt-3 rounded-lg border p-3 ${getGuidanceSeverityBg(guidance.severity)}`}
                        >
                          <div className="flex items-start justify-between gap-3">
                            <div>
                              <div className="text-sm font-medium text-ink">{guidance.title}</div>
                              <div className="mt-1 text-xs text-muted">{guidance.description}</div>
                            </div>
                            {guidance.action ? (
                              guidance.action.href ? (
                                <Link to={guidance.action.href}>
                                  <Button variant="outline" size="sm" type="button">
                                    {guidance.action.label}
                                  </Button>
                                </Link>
                              ) : guidance.action.onClick === "retry" ? (
                                <Button
                                  variant="outline"
                                  size="sm"
                                  type="button"
                                  onClick={() => retryMutation.mutate(entry.job_id)}
                                  disabled={retryMutation.isPending}
                                >
                                  {guidance.action.label}
                                </Button>
                              ) : guidance.action.onClick === "view_job" ? (
                                <Link to={`/jobs/${entry.job_id}`}>
                                  <Button variant="outline" size="sm" type="button">
                                    {guidance.action.label}
                                  </Button>
                                </Link>
                              ) : guidance.action.onClick === "view_decision" ? (
                                <Link to={`/jobs/${entry.job_id}?tab=safety`}>
                                  <Button variant="outline" size="sm" type="button">
                                    {guidance.action.label}
                                  </Button>
                                </Link>
                              ) : null
                            ) : null}
                          </div>
                        </div>
                      ) : null}
                    </div>
                  );
                })}
                {dlqQuery.hasNextPage ? (
                  <Button
                    variant="outline"
                    size="sm"
                    type="button"
                    onClick={() => dlqQuery.fetchNextPage()}
                    disabled={dlqQuery.isFetchingNextPage}
                  >
                    {dlqQuery.isFetchingNextPage ? "Loading..." : "Load more"}
                  </Button>
                ) : null}
              </div>
            )}
          </Card>
        </>
      )}

      {activeTab === "config" && (
        <>
          <Card>
            <CardHeader>
              <CardTitle>Configuration Viewer</CardTitle>
            </CardHeader>
            <div className="grid gap-3 lg:grid-cols-2">
              <div>
                <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Scope</label>
                <Select value={configScope} onChange={(event) => setConfigScope(event.target.value)}>
                  <option value="system">system</option>
                  <option value="org">org</option>
                  <option value="team">team</option>
                  <option value="workflow">workflow</option>
                  <option value="step">step</option>
                </Select>
              </div>
              <div>
                <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Scope ID</label>
                <Input
                  value={configScopeId}
                  onChange={(event) => setConfigScopeId(event.target.value)}
                  placeholder={configScope === "system" ? "default" : "scope id"}
                />
              </div>
            </div>
            <div className="mt-4 rounded-2xl border border-border bg-white/70 p-4 text-xs text-muted">
              <div className="text-xs uppercase tracking-[0.2em] text-muted">Updated</div>
              <div className="mb-2 text-sm font-semibold text-ink">
                {formatDateTime(configQuery.data?.updated_at)}
              </div>
              <Textarea readOnly rows={8} value={JSON.stringify(configQuery.data?.data || {}, null, 2)} />
            </div>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Config Diff</CardTitle>
              <div className="text-xs text-muted">Current scope vs system default</div>
            </CardHeader>
            <div className="grid gap-4 lg:grid-cols-2">
              <div>
                <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">System default</label>
                <Textarea readOnly rows={10} value={baseConfigText} />
              </div>
              <div>
                <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Current scope</label>
                <Textarea readOnly rows={10} value={currentConfigText} />
              </div>
            </div>
            <div className="mt-4 rounded-2xl border border-border bg-white/70 p-4">
              <div className="grid gap-4 lg:grid-cols-2">
                <div className="space-y-1 text-[11px] font-mono">
                  {configDiff.map((line, index) => (
                    <div
                      key={`cfg-left-${index}`}
                      className={`whitespace-pre rounded px-2 py-1 ${
                        line.match ? "text-muted" : "bg-[color:rgba(15,127,122,0.12)] text-ink"
                      }`}
                    >
                      {line.left || " "}
                    </div>
                  ))}
                </div>
                <div className="space-y-1 text-[11px] font-mono">
                  {configDiff.map((line, index) => (
                    <div
                      key={`cfg-right-${index}`}
                      className={`whitespace-pre rounded px-2 py-1 ${
                        line.match ? "text-muted" : "bg-[color:rgba(184,58,58,0.12)] text-ink"
                      }`}
                    >
                      {line.right || " "}
                    </div>
                  ))}
                </div>
              </div>
            </div>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Schemas</CardTitle>
            </CardHeader>
            {schemasQuery.data?.length ? (
              <div className="space-y-3">
                {(schemasQuery.data as Record<string, unknown>[]).map((schema, index) => (
                  <div key={`schema-${index}`} className="rounded-2xl border border-border bg-white/70 p-4">
                    <pre className="text-xs text-ink">{JSON.stringify(schema, null, 2)}</pre>
                  </div>
                ))}
              </div>
            ) : (
              <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">No schemas registered.</div>
            )}
          </Card>
        </>
      )}

      {activeTab === "observability" && (
        <>
          <Card>
            <CardHeader>
              <CardTitle>Observability Status</CardTitle>
              <div className="text-xs text-muted">OpenTelemetry export and dashboards</div>
            </CardHeader>
            <div className="grid gap-4 lg:grid-cols-2">
              <div className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="flex items-center justify-between text-xs uppercase tracking-[0.2em] text-muted">
                  <span>OpenTelemetry</span>
                  <Badge variant={otelStatusVariant}>{otelStatusLabel}</Badge>
                </div>
                <div className="mt-2 text-sm font-semibold text-ink">{otelEndpoint || "-"}</div>
                <div className="text-xs text-muted">Protocol {otelProtocol.toUpperCase()}</div>
              </div>
              <div className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="flex items-center justify-between text-xs uppercase tracking-[0.2em] text-muted">
                  <span>Grafana</span>
                  <Badge variant={grafanaStatusVariant}>{grafanaConfigured ? "configured" : "disabled"}</Badge>
                </div>
                <div className="mt-2 text-sm font-semibold text-ink">{grafanaBaseUrl || "-"}</div>
                <div className="text-xs text-muted">
                  Dashboards {grafanaSystemDashboard || grafanaWorkflowDashboard ? "set" : "not set"}
                </div>
              </div>
            </div>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>OpenTelemetry Export</CardTitle>
              <div className="text-xs text-muted">Configure where telemetry is sent</div>
            </CardHeader>
            <div className="space-y-4">
              <label className="flex items-center gap-2 text-xs text-muted">
                <input
                  type="checkbox"
                  checked={otelEnabled}
                  onChange={(event) => setOtelEnabled(event.target.checked)}
                  disabled={!canEditConfig || saveConfigMutation.isPending}
                />
                Enable OpenTelemetry export
              </label>
              <div className="grid gap-3 lg:grid-cols-2">
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Endpoint</label>
                  <Input
                    value={otelEndpoint}
                    onChange={(event) => setOtelEndpoint(event.target.value)}
                    placeholder="otel-collector:4317"
                    disabled={!canEditConfig || saveConfigMutation.isPending}
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Protocol</label>
                  <Select
                    value={otelProtocol}
                    onChange={(event) => setOtelProtocol(event.target.value)}
                    disabled={!canEditConfig || saveConfigMutation.isPending}
                  >
                    <option value="grpc">gRPC</option>
                    <option value="http">HTTP/Protobuf</option>
                  </Select>
                </div>
              </div>
              <div className="grid gap-4 lg:grid-cols-2">
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Headers (JSON)</label>
                  <Textarea
                    rows={6}
                    value={otelHeadersText}
                    onChange={(event) => setOtelHeadersText(event.target.value)}
                    placeholder='{"authorization":"Bearer ..."}'
                    disabled={!canEditConfig || saveConfigMutation.isPending}
                  />
                </div>
                <div>
                  <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Resource Attributes (JSON)</label>
                  <Textarea
                    rows={6}
                    value={otelResourceAttrsText}
                    onChange={(event) => setOtelResourceAttrsText(event.target.value)}
                    placeholder='{"service.name":"cordum-gateway"}'
                    disabled={!canEditConfig || saveConfigMutation.isPending}
                  />
                </div>
              </div>
              {observabilityError ? <div className="text-xs text-danger">{observabilityError}</div> : null}
              {!canEditConfig ? <div className="text-xs text-muted">Admin role required to update system config.</div> : null}
              <Button
                variant="primary"
                size="sm"
                type="button"
                onClick={handleSaveObservability}
                disabled={!canEditConfig || saveConfigMutation.isPending}
              >
                {saveConfigMutation.isPending ? "Saving..." : "Save Observability"}
              </Button>
            </div>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Grafana Dashboards</CardTitle>
              <div className="text-xs text-muted">Link external observability views</div>
            </CardHeader>
            <div className="space-y-4">
              <div>
                <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Base URL</label>
                <Input
                  value={grafanaBaseUrl}
                  onChange={(event) => setGrafanaBaseUrl(event.target.value)}
                  placeholder="https://grafana.example.com"
                  disabled={!canEditConfig || saveConfigMutation.isPending}
                />
              </div>
              <div className="grid gap-4 lg:grid-cols-2">
                <div className="rounded-2xl border border-border bg-white/70 p-4">
                  <div className="text-sm font-semibold text-ink">System Overview</div>
                  <div className="text-xs text-muted">CPU, Memory, and NATS message rates.</div>
                  <Input
                    className="mt-3"
                    value={grafanaSystemDashboard}
                    onChange={(event) => setGrafanaSystemDashboard(event.target.value)}
                    placeholder="d/abcd/system-overview or full URL"
                    disabled={!canEditConfig || saveConfigMutation.isPending}
                  />
                  <Button
                    className="mt-3"
                    variant="outline"
                    size="sm"
                    type="button"
                    onClick={() => window.open(grafanaSystemUrl, "_blank", "noopener,noreferrer")}
                    disabled={!grafanaSystemUrl}
                  >
                    Open Dashboard
                  </Button>
                </div>
                <div className="rounded-2xl border border-border bg-white/70 p-4">
                  <div className="text-sm font-semibold text-ink">Workflow Performance</div>
                  <div className="text-xs text-muted">Latency, success rates, and cost per run.</div>
                  <Input
                    className="mt-3"
                    value={grafanaWorkflowDashboard}
                    onChange={(event) => setGrafanaWorkflowDashboard(event.target.value)}
                    placeholder="d/abcd/workflow-performance or full URL"
                    disabled={!canEditConfig || saveConfigMutation.isPending}
                  />
                  <Button
                    className="mt-3"
                    variant="outline"
                    size="sm"
                    type="button"
                    onClick={() => window.open(grafanaWorkflowUrl, "_blank", "noopener,noreferrer")}
                    disabled={!grafanaWorkflowUrl}
                  >
                    Open Dashboard
                  </Button>
                </div>
              </div>
              <Button
                variant="primary"
                size="sm"
                type="button"
                onClick={handleSaveObservability}
                disabled={!canEditConfig || saveConfigMutation.isPending}
              >
                {saveConfigMutation.isPending ? "Saving..." : "Save Grafana Links"}
              </Button>
            </div>
          </Card>
        </>
      )}

      {activeTab === "alerting" && (
        <>
          <Card>
            <CardHeader>
              <CardTitle>Alert Destinations</CardTitle>
              <div className="text-xs text-muted">Route critical events to on-call systems</div>
            </CardHeader>
            <div className="space-y-4">
              <div className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="flex items-center justify-between mb-2">
                  <div className="text-sm font-semibold text-ink">PagerDuty Integration</div>
                  <Badge variant={pagerDutyStatusVariant}>{pagerDutyStatusLabel}</Badge>
                </div>
                <label className="flex items-center gap-2 text-xs text-muted">
                  <input
                    type="checkbox"
                    checked={pagerDutyEnabled}
                    onChange={(event) => setPagerDutyEnabled(event.target.checked)}
                    disabled={!canEditConfig || saveConfigMutation.isPending}
                  />
                  Enable PagerDuty alerts
                </label>
                <div className="grid gap-3 lg:grid-cols-2">
                  <Input
                    type="password"
                    placeholder="Integration Key"
                    value={pagerDutyKey}
                    onChange={(event) => setPagerDutyKey(event.target.value)}
                    disabled={!canEditConfig || saveConfigMutation.isPending}
                  />
                  <Select
                    value={pagerDutySeverity}
                    onChange={(event) => setPagerDutySeverity(event.target.value as AlertSeverity)}
                    disabled={!canEditConfig || saveConfigMutation.isPending}
                  >
                    {SEVERITY_OPTIONS.map((option) => (
                      <option key={option} value={option}>
                        {option}
                      </option>
                    ))}
                  </Select>
                </div>
              </div>
              
              <div className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="flex items-center justify-between mb-2">
                  <div className="text-sm font-semibold text-ink">Slack Notifications</div>
                  <Badge variant={slackStatusVariant}>{slackStatusLabel}</Badge>
                </div>
                <label className="flex items-center gap-2 text-xs text-muted">
                  <input
                    type="checkbox"
                    checked={slackEnabled}
                    onChange={(event) => setSlackEnabled(event.target.checked)}
                    disabled={!canEditConfig || saveConfigMutation.isPending}
                  />
                  Enable Slack alerts
                </label>
                <div className="grid gap-3 lg:grid-cols-2">
                  <Input
                    type="password"
                    placeholder="https://hooks.slack.com/services/..."
                    value={slackWebhook}
                    onChange={(event) => setSlackWebhook(event.target.value)}
                    disabled={!canEditConfig || saveConfigMutation.isPending}
                  />
                  <Select
                    value={slackSeverity}
                    onChange={(event) => setSlackSeverity(event.target.value as AlertSeverity)}
                    disabled={!canEditConfig || saveConfigMutation.isPending}
                  >
                    {SEVERITY_OPTIONS.map((option) => (
                      <option key={option} value={option}>
                        {option}
                      </option>
                    ))}
                  </Select>
                </div>
              </div>

              <div className="flex justify-end">
                <Button
                  variant="primary"
                  size="sm"
                  type="button"
                  onClick={handleSaveAlerting}
                  disabled={!canEditConfig || saveConfigMutation.isPending}
                >
                  {saveConfigMutation.isPending ? "Saving..." : "Save Alerting"}
                </Button>
              </div>
              {alertingError ? <div className="text-xs text-danger">{alertingError}</div> : null}
              {!canEditConfig ? <div className="text-xs text-muted">Admin role required to update system config.</div> : null}
            </div>
          </Card>
        </>
      )}
    </div>
  );
}
