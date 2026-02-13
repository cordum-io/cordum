import { useMemo } from "react";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { Badge } from "../ui/Badge";
import { ProgressBar } from "../ProgressBar";
import type { Worker } from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function statusVariant(
  status: string,
): "success" | "warning" | "danger" | "default" {
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

function relativeTime(iso?: string): string {
  if (!iso) return "\u2014";
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 0) return "just now";
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

// ---------------------------------------------------------------------------
// Worker card
// ---------------------------------------------------------------------------

function WorkerCard({ worker, onClick }: { worker: Worker; onClick?: () => void }) {
  const loadPct =
    worker.capacity > 0
      ? Math.round((worker.activeJobs / worker.capacity) * 100)
      : 0;

  return (
    <Card className={onClick ? "cursor-pointer" : undefined} onClick={onClick}>
      <CardHeader>
        <CardTitle className="text-sm">{worker.name}</CardTitle>
        <Badge variant={statusVariant(worker.status)}>{worker.status}</Badge>
      </CardHeader>
      <div className="space-y-2 text-xs text-muted">
        <div className="flex items-center justify-between">
          <span>
            {worker.activeJobs}/{worker.capacity} jobs
          </span>
          <span>{loadPct}%</span>
        </div>
        <ProgressBar
          value={loadPct}
          variant={loadPct >= 90 ? "danger" : loadPct >= 70 ? "warning" : "default"}
        />
        <div>Heartbeat: {relativeTime(worker.lastHeartbeat)}</div>
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Pool group
// ---------------------------------------------------------------------------

interface PoolGroup {
  pool: string;
  workers: Worker[];
  totalActive: number;
  totalCapacity: number;
}

function PoolSection({ group, onWorkerClick }: { group: PoolGroup; onWorkerClick?: (id: string) => void }) {
  const utilPct =
    group.totalCapacity > 0
      ? Math.round((group.totalActive / group.totalCapacity) * 100)
      : 0;

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="font-display text-lg font-semibold text-ink">
          {group.pool}
        </h3>
        <span className="text-xs text-muted">
          {group.workers.length} worker{group.workers.length !== 1 ? "s" : ""}{" "}
          &middot; {group.totalActive}/{group.totalCapacity} active &middot;{" "}
          {utilPct}%
        </span>
      </div>
      <ProgressBar
        value={utilPct}
        variant={utilPct >= 90 ? "danger" : utilPct >= 70 ? "warning" : "default"}
        className="h-2.5"
      />
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
        {group.workers.map((w) => (
          <WorkerCard key={w.id} worker={w} onClick={onWorkerClick ? () => onWorkerClick(w.id) : undefined} />
        ))}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// PoolGroupedView
// ---------------------------------------------------------------------------

interface PoolGroupedViewProps {
  workers: Worker[];
  onWorkerClick?: (id: string) => void;
}

export function PoolGroupedView({ workers, onWorkerClick }: PoolGroupedViewProps) {
  const groups = useMemo(() => {
    const map = new Map<string, Worker[]>();
    for (const w of workers) {
      const list = map.get(w.pool) ?? [];
      list.push(w);
      map.set(w.pool, list);
    }
    return [...map.entries()]
      .sort(([a], [b]) => a.localeCompare(b))
      .map(
        ([pool, wks]): PoolGroup => ({
          pool,
          workers: wks,
          totalActive: wks.reduce((s, w) => s + w.activeJobs, 0),
          totalCapacity: wks.reduce((s, w) => s + w.capacity, 0),
        }),
      );
  }, [workers]);

  return (
    <div className="space-y-8">
      {groups.map((g) => (
        <PoolSection key={g.pool} group={g} onWorkerClick={onWorkerClick} />
      ))}
    </div>
  );
}
