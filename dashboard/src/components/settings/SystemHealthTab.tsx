import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { get } from "../../api/client";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { Badge } from "../ui/Badge";
import { ProgressBar } from "../ProgressBar";
import { cn } from "../../lib/utils";
import {
  Loader,
  CheckCircle,
  AlertTriangle,
  XCircle,
  RefreshCw,
  ChevronDown,
  ChevronUp,
} from "lucide-react";
import { useStatus } from "../../hooks/useStatus";
import { ReplicaTable } from "./ReplicaTable";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ComponentHealth {
  name: string;
  status: "healthy" | "degraded" | "down";
  version?: string;
  uptime?: number;
  latencyMs?: number;
  details?: Record<string, unknown>;
}

export interface GatewayStatus {
  time?: string;
  uptime_seconds?: number;
  build?: { version?: string; commit?: string; date?: string };
  nats?: { connected?: boolean; status?: string; url?: string; latency_ms?: number };
  redis?: { ok?: boolean; error?: string; latency_ms?: number };
  workers?: { count?: number };
  output_policy?: { enabled?: boolean; fail_mode?: string };
}

export interface SystemHealth {
  overall: "healthy" | "degraded" | "down";
  components: ComponentHealth[];
  checkedAt: string;
}

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

function useSystemHealth() {
  return useQuery<SystemHealth>({
    queryKey: ["system-health"],
    queryFn: async () => {
      const statusPromise = get<GatewayStatus>("/status");
      const configPromise = get<Record<string, unknown>>("/config").catch(() => undefined);
      const [status, cfg] = await Promise.all([statusPromise, configPromise]);
      return mapGatewayStatus(status, cfg);
    },
    refetchInterval: 30_000,
    staleTime: 25_000,
  });
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

export function formatUptime(seconds?: number): string {
  if (seconds == null) return "\u2014";
  if (seconds < 60) return `${seconds}s`;
  const mins = Math.floor(seconds / 60);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  const remainMins = mins % 60;
  if (hrs < 24) return remainMins > 0 ? `${hrs}h ${remainMins}m` : `${hrs}h`;
  const days = Math.floor(hrs / 24);
  const remainHrs = hrs % 24;
  return remainHrs > 0 ? `${days}d ${remainHrs}h` : `${days}d`;
}

function timeAgo(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1_000);
  if (secs < 5) return "just now";
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  return `${Math.floor(mins / 60)}h ago`;
}

function statusIcon(status: string) {
  switch (status) {
    case "healthy":
      return <CheckCircle className="h-5 w-5 text-success" />;
    case "degraded":
      return <AlertTriangle className="h-5 w-5 text-warning" />;
    default:
      return <XCircle className="h-5 w-5 text-danger" />;
  }
}

export function statusVariant(
  status: string,
): "success" | "warning" | "danger" {
  switch (status) {
    case "healthy":
      return "success";
    case "degraded":
      return "warning";
    default:
      return "danger";
  }
}

const BORDER_COLOR: Record<string, string> = {
  healthy: "border-success/30",
  degraded: "border-warning/30",
  down: "border-danger/30 animate-pulse",
};

export function parseMaybeBool(value: unknown): boolean | undefined {
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

export function parseOutputPolicy(
  status: GatewayStatus,
  cfg?: Record<string, unknown>,
): { enabled?: boolean; failMode?: string } {
  const fromStatus = status.output_policy;
  if (fromStatus && typeof fromStatus === "object") {
    return {
      enabled: parseMaybeBool(fromStatus.enabled),
      failMode: typeof fromStatus.fail_mode === "string" ? fromStatus.fail_mode : undefined,
    };
  }

  if (!cfg || typeof cfg !== "object") {
    return {};
  }

  const fromNested = cfg.output_policy as Record<string, unknown> | undefined;
  if (fromNested && typeof fromNested === "object") {
    return {
      enabled: parseMaybeBool(fromNested.enabled),
      failMode: typeof fromNested.fail_mode === "string" ? fromNested.fail_mode : undefined,
    };
  }

  return {
    enabled: parseMaybeBool(cfg.output_policy_enabled ?? cfg.outputPolicyEnabled),
    failMode:
      typeof cfg.output_policy_fail_mode === "string"
        ? cfg.output_policy_fail_mode
        : typeof cfg.outputPolicyFailMode === "string"
          ? cfg.outputPolicyFailMode
          : undefined,
  };
}

export function mapGatewayStatus(status: GatewayStatus, cfg?: Record<string, unknown>): SystemHealth {
  const components: ComponentHealth[] = [];

  const redisOk = status.redis?.ok ?? false;
  components.push({
    name: "Redis",
    status: redisOk ? "healthy" : "down",
    latencyMs: status.redis?.latency_ms,
    details: { error: status.redis?.error },
  });

  const natsConnected = status.nats?.connected ?? false;
  components.push({
    name: "NATS",
    status: natsConnected ? "healthy" : "degraded",
    latencyMs: status.nats?.latency_ms,
    details: { status: status.nats?.status, url: status.nats?.url },
  });

  components.push({
    name: "Workers",
    status: (status.workers?.count ?? 0) > 0 ? "healthy" : "degraded",
    details: { count: status.workers?.count ?? 0 },
  });

  components.push({
    name: "Gateway",
    status: "healthy",
    version: status.build?.version,
    uptime: status.uptime_seconds,
    details: { commit: status.build?.commit, date: status.build?.date },
  });

  const outputPolicy = parseOutputPolicy(status, cfg);
  components.push({
    name: "Output Policy",
    status: outputPolicy.enabled === true ? "healthy" : "degraded",
    details: {
      enabled: outputPolicy.enabled,
      fail_mode: outputPolicy.failMode,
    },
  });

  const down = components.filter((c) => c.status === "down").length;
  const degraded = components.filter((c) => c.status === "degraded").length;
  const overall: SystemHealth["overall"] =
    down > 0 ? "down" : degraded > 0 ? "degraded" : "healthy";

  return { overall, components, checkedAt: new Date().toISOString() };
}

// ---------------------------------------------------------------------------
// Latency Sparkline (inline SVG, 60x20)
// ---------------------------------------------------------------------------

const SPARKLINE_MAX_POINTS = 30;

function LatencySparkline({ points }: { points: number[] }) {
  if (points.length < 2) return null;
  const max = Math.max(...points, 1);
  const w = 60;
  const h = 20;
  const step = w / (points.length - 1);
  const d = points
    .map((v, i) => `${i === 0 ? "M" : "L"}${(i * step).toFixed(1)},${(h - (v / max) * (h - 2) - 1).toFixed(1)}`)
    .join(" ");

  return (
    <svg width={w} height={h} className="shrink-0" aria-label="Latency trend">
      <polyline
        points={d.replace(/[ML]/g, "").replace(/,/g, " ").trim().split("  ").join(" ")}
        fill="none"
        stroke="currentColor"
        strokeWidth={1.5}
        className="text-accent"
      />
    </svg>
  );
}

// ---------------------------------------------------------------------------
// Overall summary with refresh button
// ---------------------------------------------------------------------------

function OverallSummary({
  health,
  onRefresh,
  isRefreshing,
}: {
  health: SystemHealth;
  onRefresh: () => void;
  isRefreshing: boolean;
}) {
  const healthy = health.components.filter((c) => c.status === "healthy").length;
  const total = health.components.length;
  const pct = total > 0 ? Math.round((healthy / total) * 100) : 0;

  return (
    <Card className={cn("border-l-4", BORDER_COLOR[health.overall])}>
      <div className="flex items-center gap-4">
        {statusIcon(health.overall)}
        <div className="flex-1">
          <p className="text-sm font-semibold text-ink">
            {health.overall === "healthy"
              ? "All systems operational"
              : health.overall === "degraded"
                ? `${total - healthy} component${total - healthy !== 1 ? "s" : ""} degraded`
                : `${total - healthy} component${total - healthy !== 1 ? "s" : ""} down`}
          </p>
          <p className="text-xs text-muted">
            {healthy}/{total} components healthy &middot; checked {timeAgo(health.checkedAt)}
          </p>
        </div>
        <button
          type="button"
          onClick={onRefresh}
          disabled={isRefreshing}
          className="rounded-lg p-2 text-muted hover:text-ink hover:bg-surface2 transition-colors disabled:opacity-50"
          title="Refresh now"
        >
          <RefreshCw className={cn("h-4 w-4", isRefreshing && "animate-spin")} />
        </button>
        <Badge variant={statusVariant(health.overall)}>
          {health.overall}
        </Badge>
      </div>
      <ProgressBar
        value={pct}
        variant={statusVariant(health.overall)}
        className="mt-3"
      />
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Enhanced component card with sparkline
// ---------------------------------------------------------------------------

function ComponentCard({
  component,
  latencyHistory,
}: {
  component: ComponentHealth;
  latencyHistory: number[];
}) {
  const address = component.details?.address as string | undefined;
  const port = component.details?.port as number | undefined;
  const outputEnabled = component.details?.enabled as boolean | undefined;
  const outputFailMode = component.details?.fail_mode as string | undefined;

  return (
    <Card className={cn(component.status === "down" && "animate-pulse")}>
      <CardHeader>
        <div className="flex items-center gap-2">
          {statusIcon(component.status)}
          <CardTitle className="text-sm">{component.name}</CardTitle>
        </div>
        <Badge variant={statusVariant(component.status)}>
          {component.status}
        </Badge>
      </CardHeader>
      <div className="space-y-1.5 text-xs text-muted">
        {component.version && (
          <div className="flex items-center justify-between">
            <span>Version</span>
            <Badge variant="info" className="text-[10px]">{component.version}</Badge>
          </div>
        )}
        <div className="flex justify-between">
          <span>Uptime</span>
          <span className="text-ink">{formatUptime(component.uptime)}</span>
        </div>
        {component.latencyMs != null && (
          <div className="flex items-center justify-between">
            <span>Latency</span>
            <div className="flex items-center gap-2">
              <LatencySparkline points={latencyHistory} />
              <span className="font-mono text-ink">{component.latencyMs}ms</span>
            </div>
          </div>
        )}
        {(address || port) && (
          <div className="flex justify-between">
            <span>Connection</span>
            <span className="font-mono text-ink">
              {address ?? ""}
              {port ? `:${port}` : ""}
            </span>
          </div>
        )}
        {component.name === "Output Policy" && (
          <>
            <div className="flex justify-between">
              <span>Enabled</span>
              <span className="text-ink">
                {outputEnabled === undefined ? "Unknown" : outputEnabled ? "Yes" : "No"}
              </span>
            </div>
            <div className="flex justify-between">
              <span>Fail mode</span>
              <span className="text-ink">{outputFailMode || "open"}</span>
            </div>
          </>
        )}
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Loading skeleton
// ---------------------------------------------------------------------------

function HealthSkeleton() {
  return (
    <div className="space-y-4">
      <Card className="animate-pulse">
        <div className="flex items-center gap-4">
          <div className="h-5 w-5 rounded-full bg-surface2" />
          <div className="flex-1 space-y-2">
            <div className="h-4 w-48 rounded bg-surface2" />
            <div className="h-3 w-32 rounded bg-surface2" />
          </div>
        </div>
        <div className="mt-3 h-2 rounded bg-surface2" />
      </Card>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {Array.from({ length: 4 }, (_, i) => (
          <Card key={i} className="animate-pulse">
            <div className="space-y-3">
              <div className="h-5 w-1/3 rounded bg-surface2" />
              <div className="h-4 w-2/3 rounded bg-surface2" />
              <div className="h-4 w-1/2 rounded bg-surface2" />
            </div>
          </Card>
        ))}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Collapsible section (default collapsed)
// ---------------------------------------------------------------------------

function CollapsibleSection({
  title,
  children,
  defaultOpen = false,
}: {
  title: string;
  children: React.ReactNode;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);

  return (
    <div>
      <button
        type="button"
        className="flex w-full items-center justify-between rounded-xl border border-border bg-surface px-4 py-2.5 text-left transition-colors hover:bg-surface2/50"
        onClick={() => setOpen((v) => !v)}
      >
        <span className="text-xs font-semibold text-ink">{title}</span>
        {open ? (
          <ChevronUp className="h-4 w-4 text-muted" />
        ) : (
          <ChevronDown className="h-4 w-4 text-muted" />
        )}
      </button>
      {open && <div className="mt-3">{children}</div>}
    </div>
  );
}

// ---------------------------------------------------------------------------
// SystemHealthTab (exported)
// ---------------------------------------------------------------------------

export function SystemHealthTab() {
  const queryClient = useQueryClient();
  const { data, isLoading, isFetching, error } = useSystemHealth();
  const { data: statusData } = useStatus();

  // Track latency history per component (last 30 data points)
  const latencyHistoryRef = useRef<Record<string, number[]>>({});

  // Append latency on each data fetch
  if (data) {
    for (const comp of data.components) {
      if (comp.latencyMs != null) {
        const history = latencyHistoryRef.current[comp.name] ?? [];
        // Only append if the last value differs (avoid duplicates on re-render)
        if (history.length === 0 || history[history.length - 1] !== comp.latencyMs) {
          history.push(comp.latencyMs);
          if (history.length > SPARKLINE_MAX_POINTS) history.shift();
          latencyHistoryRef.current[comp.name] = history;
        }
      }
    }
  }

  const handleRefresh = () => {
    queryClient.invalidateQueries({ queryKey: ["system-health"] });
  };

  if (isLoading) {
    return <HealthSkeleton />;
  }

  if (error || !data) {
    return (
      <Card>
        <p className="py-8 text-center text-sm text-danger">
          Failed to load system health.
        </p>
      </Card>
    );
  }

  return (
    <div className="space-y-4">
      <OverallSummary
        health={data}
        onRefresh={handleRefresh}
        isRefreshing={isFetching}
      />

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {data.components.map((comp) => (
          <ComponentCard
            key={comp.name}
            component={comp}
            latencyHistory={latencyHistoryRef.current[comp.name] ?? []}
          />
        ))}
      </div>

      {/* Service replicas (hidden gracefully in single-replica mode) */}
      <ReplicaTable replicas={statusData?.replicas} />

      <p className="text-[11px] text-muted">
        Auto-refreshes every 30 seconds.
      </p>
    </div>
  );
}
