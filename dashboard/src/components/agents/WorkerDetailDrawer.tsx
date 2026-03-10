import { X } from "lucide-react";
import { Drawer } from "../ui/Drawer";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { useWorker, useWorkerJobs } from "../../hooks/useWorkers";
import type { Job } from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function statusVariant(status: string): "success" | "warning" | "danger" | "default" {
  switch (status) {
    case "online":
    case "active":
      return "success";
    case "draining":
      return "warning";
    case "offline":
    case "error":
      return "danger";
    default:
      return "default";
  }
}

function jobStatusVariant(status: string): "success" | "warning" | "danger" | "info" | "default" {
  switch (status) {
    case "succeeded":
      return "success";
    case "running":
    case "dispatched":
      return "info";
    case "failed":
      return "danger";
    case "pending":
      return "warning";
    default:
      return "default";
  }
}

function formatDuration(ms?: number): string {
  if (ms == null) return "--";
  if (ms < 1_000) return `${ms}ms`;
  const secs = Math.floor(ms / 1_000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  return `${mins}m ${secs % 60}s`;
}

function formatUptime(seconds?: number): string {
  if (seconds == null) return "--";
  if (seconds < 60) return `${seconds}s`;
  const mins = Math.floor(seconds / 60);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ${mins % 60}m`;
  const days = Math.floor(hrs / 24);
  return `${days}d ${hrs % 24}h`;
}

function relativeTime(iso?: string): string {
  if (!iso) return "--";
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1_000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  return `${hrs}h ago`;
}

function heartbeatAge(iso?: string): { seconds: number; label: string; color: "success" | "warning" | "danger" } | null {
  if (!iso) return null;
  const diff = Date.now() - new Date(iso).getTime();
  if (isNaN(diff) || diff < 0) return null;
  const secs = Math.floor(diff / 1_000);
  let label: string;
  if (secs < 60) label = `${secs}s ago`;
  else if (secs < 3600) label = `${Math.floor(secs / 60)}m ago`;
  else label = `${Math.floor(secs / 3600)}h ago`;
  const color = secs < 30 ? "success" : secs < 120 ? "warning" : "danger";
  return { seconds: secs, label, color };
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function WorkerDetailDrawer({
  workerId,
  onClose,
}: {
  workerId: string | null;
  onClose: () => void;
}) {
  const { data: worker, isLoading } = useWorker(workerId);
  const { data: jobs } = useWorkerJobs(workerId);

  const hbAge = heartbeatAge(worker?.lastHeartbeat);

  return (
    <Drawer open={!!workerId} onClose={onClose} size="lg">
      <div className="space-y-6">
        {/* Close button */}
        <div className="flex items-center justify-between">
          <h2 className="font-display text-lg font-bold text-ink">
            Worker Detail
          </h2>
          <Button variant="ghost" size="sm" onClick={onClose}>
            <X className="h-4 w-4" />
          </Button>
        </div>

        {isLoading && (
          <div className="space-y-4 animate-pulse">
            <div className="h-6 w-1/2 rounded bg-surface2" />
            <div className="h-4 w-3/4 rounded bg-surface2" />
            <div className="h-32 rounded bg-surface2" />
          </div>
        )}

        {!isLoading && !worker && (
          <p className="text-sm text-muted-foreground">Worker not found.</p>
        )}

        {worker && (
          <>
            {/* Header */}
            <div className="space-y-1">
              <div className="flex items-center gap-3">
                <h3 className="text-lg font-semibold text-ink">{worker.name}</h3>
                <Badge variant={statusVariant(worker.status)}>{worker.status}</Badge>
              </div>
              <p className="text-sm text-muted-foreground">Pool: {worker.pool}</p>
            </div>

            {/* Capabilities */}
            <section className="space-y-2">
              <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Capabilities
              </h4>
              <div className="flex flex-wrap gap-1.5">
                {worker.capabilities.length > 0 ? (
                  worker.capabilities.map((cap) => (
                    <Badge key={cap} variant="info">{cap}</Badge>
                  ))
                ) : (
                  <span className="text-xs text-muted-foreground">None</span>
                )}
              </div>
            </section>

            {/* Connection Details */}
            <section className="space-y-2">
              <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Connection
              </h4>
              <div className="grid grid-cols-2 gap-3 text-sm">
                <div>
                  <span className="text-xs text-muted-foreground">Address</span>
                  <p className="font-mono text-ink">{worker.address ?? "--"}</p>
                </div>
                <div>
                  <span className="text-xs text-muted-foreground">Version</span>
                  <p className="text-ink">{worker.version ?? "--"}</p>
                </div>
                <div>
                  <span className="text-xs text-muted-foreground">Uptime</span>
                  <p className="text-ink">{formatUptime(worker.uptime)}</p>
                </div>
                <div>
                  <span className="text-xs text-muted-foreground">Last Heartbeat</span>
                  <p className="text-ink">{relativeTime(worker.lastHeartbeat)}</p>
                </div>
                <div>
                  <span className="text-xs text-muted-foreground">Load</span>
                  <p className="text-ink">{worker.activeJobs} / {worker.capacity}</p>
                </div>
              </div>
            </section>

            {/* Heartbeat Status */}
            <section className="space-y-2">
              <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Heartbeat Status
              </h4>
              {hbAge ? (
                <div className="flex items-center gap-3 rounded-xl bg-surface2/30 px-4 py-3">
                  <span
                    className={`h-3 w-3 rounded-full ${
                      hbAge.color === "success"
                        ? "bg-success"
                        : hbAge.color === "warning"
                          ? "bg-warning"
                          : "bg-danger"
                    }`}
                  />
                  <div>
                    <p className="text-sm font-semibold text-ink">
                      Last seen {hbAge.label}
                    </p>
                    <p className="text-[11px] text-muted-foreground">
                      {worker.lastHeartbeat}
                    </p>
                  </div>
                </div>
              ) : (
                <p className="text-xs text-muted-foreground">No heartbeat data available.</p>
              )}
            </section>

            {/* Recent Jobs */}
            <section className="space-y-2">
              <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Recent Jobs (last 20)
              </h4>
              {!jobs || jobs.length === 0 ? (
                <p className="text-xs text-muted-foreground">No recent jobs.</p>
              ) : (
                <div className="overflow-x-auto rounded-xl border border-border">
                  <table className="w-full text-xs">
                    <thead className="border-b border-border bg-surface2/30">
                      <tr>
                        <th className="px-3 py-2 text-left font-semibold text-muted-foreground">Job ID</th>
                        <th className="px-3 py-2 text-left font-semibold text-muted-foreground">Status</th>
                        <th className="px-3 py-2 text-left font-semibold text-muted-foreground">Topic</th>
                        <th className="px-3 py-2 text-left font-semibold text-muted-foreground">Duration</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-border">
                      {jobs.map((job: Job) => (
                        <tr key={job.id} className="hover:bg-surface2/20">
                          <td className="px-3 py-2 font-mono text-ink">
                            {job.id.slice(0, 8)}
                          </td>
                          <td className="px-3 py-2">
                            <Badge variant={jobStatusVariant(job.status)}>
                              {job.status}
                            </Badge>
                          </td>
                          <td className="px-3 py-2 text-muted-foreground font-mono">
                            {job.topic}
                          </td>
                          <td className="px-3 py-2 text-muted-foreground">
                            {formatDuration(job.duration)}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </section>
          </>
        )}
      </div>
    </Drawer>
  );
}
