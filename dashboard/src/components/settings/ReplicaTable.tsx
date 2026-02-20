import { useState } from "react";
import { ChevronDown, ChevronUp, Server, Info } from "lucide-react";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { Badge } from "../ui/Badge";
import { cn } from "../../lib/utils";
import type { ReplicaInfo } from "../../hooks/useStatus";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const STALE_THRESHOLD_MS = 30_000;

function isStale(lastSeen: string): boolean {
  const diff = Date.now() - new Date(lastSeen).getTime();
  return diff > STALE_THRESHOLD_MS;
}

function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 0) return "just now";
  const secs = Math.floor(diff / 1_000);
  if (secs < 5) return "just now";
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

// ---------------------------------------------------------------------------
// Service group (collapsible)
// ---------------------------------------------------------------------------

function ServiceGroup({
  service,
  instances,
}: {
  service: string;
  instances: ReplicaInfo[];
}) {
  const [open, setOpen] = useState(true);
  const healthyCount = instances.filter((r) => !isStale(r.last_seen)).length;
  const total = instances.length;
  const allHealthy = healthyCount === total;

  return (
    <div className="rounded-xl border border-border overflow-hidden">
      <button
        type="button"
        className="flex w-full items-center justify-between bg-surface px-4 py-2.5 text-left transition-colors hover:bg-surface2/50"
        onClick={() => setOpen((v) => !v)}
      >
        <div className="flex items-center gap-2">
          <Server className="h-4 w-4 text-muted" />
          <span className="text-sm font-semibold text-ink">{service}</span>
        </div>
        <div className="flex items-center gap-2">
          <Badge variant={allHealthy ? "success" : "warning"}>
            {healthyCount}/{total} healthy
          </Badge>
          {open ? (
            <ChevronUp className="h-4 w-4 text-muted" />
          ) : (
            <ChevronDown className="h-4 w-4 text-muted" />
          )}
        </div>
      </button>

      {open && (
        <table className="w-full text-xs">
          <thead>
            <tr className="border-t border-border bg-surface2/30 text-left text-muted">
              <th className="px-4 py-2 font-medium">Instance ID</th>
              <th className="px-4 py-2 font-medium">Version</th>
              <th className="px-4 py-2 font-medium">Uptime</th>
              <th className="px-4 py-2 font-medium">Last Seen</th>
              <th className="px-4 py-2 font-medium">Status</th>
            </tr>
          </thead>
          <tbody>
            {instances.map((replica) => {
              const stale = isStale(replica.last_seen);
              return (
                <tr
                  key={replica.id}
                  className="border-t border-border/50 transition-colors hover:bg-surface2/20"
                >
                  <td className="px-4 py-2 font-mono text-ink">{replica.id}</td>
                  <td className="px-4 py-2 text-muted">{replica.version || "\u2014"}</td>
                  <td className="px-4 py-2 text-muted">{replica.uptime || "\u2014"}</td>
                  <td className="px-4 py-2 text-muted">{relativeTime(replica.last_seen)}</td>
                  <td className="px-4 py-2">
                    <span className="flex items-center gap-1.5">
                      <span
                        className={cn(
                          "h-2 w-2 rounded-full",
                          stale ? "bg-danger" : "bg-success",
                        )}
                      />
                      <span className={stale ? "text-danger" : "text-success"}>
                        {stale ? "Stale" : "Healthy"}
                      </span>
                    </span>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// ReplicaTable (exported)
// ---------------------------------------------------------------------------

export function ReplicaTable({
  replicas,
}: {
  replicas?: Record<string, ReplicaInfo[]>;
}) {
  // Graceful degradation: single-replica mode
  if (!replicas) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-sm">Service Replicas</CardTitle>
        </CardHeader>
        <div className="flex items-center gap-2 text-sm text-muted">
          <Info className="h-4 w-4" />
          Single-replica mode — instance registry not available
        </div>
      </Card>
    );
  }

  const services = Object.entries(replicas).filter(
    ([, instances]) => instances.length > 0,
  );

  if (services.length === 0) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-sm">Service Replicas</CardTitle>
        </CardHeader>
        <p className="text-sm text-muted">No registered instances.</p>
      </Card>
    );
  }

  return (
    <div className="space-y-3">
      <h3 className="text-sm font-semibold text-ink">Service Replicas</h3>
      {services.map(([service, instances]) => (
        <ServiceGroup key={service} service={service} instances={instances} />
      ))}
    </div>
  );
}
