import { useQuery } from "@tanstack/react-query";
import { get } from "../../api/client";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { Badge } from "../ui/Badge";
import { ProgressBar } from "../ProgressBar";
import { Loader, CheckCircle, AlertTriangle, XCircle } from "lucide-react";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface ComponentHealth {
  name: string;
  status: "healthy" | "degraded" | "down";
  version?: string;
  uptime?: number;
  details?: Record<string, unknown>;
}

interface GatewayStatus {
  time?: string;
  uptime_seconds?: number;
  build?: { version?: string; commit?: string; date?: string };
  nats?: { connected?: boolean; status?: string; url?: string };
  redis?: { ok?: boolean; error?: string };
  workers?: { count?: number };
}

interface SystemHealth {
  overall: "healthy" | "degraded" | "down";
  components: ComponentHealth[];
}

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

function useSystemHealth() {
  return useQuery<SystemHealth>({
    queryKey: ["system-health"],
    queryFn: async () => {
      const status = await get<GatewayStatus>("/status");
      return mapGatewayStatus(status);
    },
    refetchInterval: 30_000,
    staleTime: 25_000,
  });
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatUptime(seconds?: number): string {
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

function statusVariant(
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

function mapGatewayStatus(status: GatewayStatus): SystemHealth {
  const components: ComponentHealth[] = [];

  const redisOk = status.redis?.ok ?? false;
  components.push({
    name: "Redis",
    status: redisOk ? "healthy" : "down",
    details: { error: status.redis?.error },
  });

  const natsConnected = status.nats?.connected ?? false;
  components.push({
    name: "NATS",
    status: natsConnected ? "healthy" : "degraded",
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

  const down = components.filter((c) => c.status === "down").length;
  const degraded = components.filter((c) => c.status === "degraded").length;
  const overall: SystemHealth["overall"] =
    down > 0 ? "down" : degraded > 0 ? "degraded" : "healthy";

  return { overall, components };
}

// ---------------------------------------------------------------------------
// Overall summary
// ---------------------------------------------------------------------------

function OverallSummary({ health }: { health: SystemHealth }) {
  const healthy = health.components.filter((c) => c.status === "healthy").length;
  const total = health.components.length;
  const pct = total > 0 ? Math.round((healthy / total) * 100) : 0;

  return (
    <Card>
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
            {healthy}/{total} components healthy
          </p>
        </div>
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
// Component card
// ---------------------------------------------------------------------------

function ComponentCard({ component }: { component: ComponentHealth }) {
  const address = component.details?.address as string | undefined;
  const port = component.details?.port as number | undefined;

  return (
    <Card>
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
          <div className="flex justify-between">
            <span>Version</span>
            <span className="font-mono text-ink">{component.version}</span>
          </div>
        )}
        <div className="flex justify-between">
          <span>Uptime</span>
          <span className="text-ink">{formatUptime(component.uptime)}</span>
        </div>
        {(address || port) && (
          <div className="flex justify-between">
            <span>Connection</span>
            <span className="font-mono text-ink">
              {address ?? ""}
              {port ? `:${port}` : ""}
            </span>
          </div>
        )}
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// SystemHealthTab (exported)
// ---------------------------------------------------------------------------

export function SystemHealthTab() {
  const { data, isLoading, error } = useSystemHealth();

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-16 text-sm text-muted">
        <Loader className="mr-2 h-4 w-4 animate-spin" />
        Loading system health...
      </div>
    );
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
      <OverallSummary health={data} />

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {data.components.map((comp) => (
          <ComponentCard key={comp.name} component={comp} />
        ))}
      </div>

      <p className="text-[11px] text-muted">
        Auto-refreshes every 30 seconds.
      </p>
    </div>
  );
}
