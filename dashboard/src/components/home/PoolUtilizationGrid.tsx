import { useMemo } from "react";
import { Server } from "lucide-react";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { ProgressBar } from "../ProgressBar";
import { useWorkers } from "../../hooks/useWorkers";
import { cn } from "../../lib/utils";
import type { Worker } from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

interface PoolGroup {
  pool: string;
  workers: Worker[];
  activeJobs: number;
  totalCapacity: number;
  utilization: number;
}

function utilizationVariant(pct: number): "success" | "warning" | "danger" {
  if (pct > 80) return "danger";
  if (pct >= 50) return "warning";
  return "success";
}

function statusDot(status: string) {
  const color =
    status === "online" || status === "active"
      ? "bg-success"
      : status === "draining"
        ? "bg-warning"
        : "bg-muted";
  return <span className={cn("inline-block h-2 w-2 rounded-full", color)} />;
}

// ---------------------------------------------------------------------------
// Pool card
// ---------------------------------------------------------------------------

function PoolCard({ group }: { group: PoolGroup }) {
  const variant = utilizationVariant(group.utilization);

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2">
          <Server className="h-4 w-4 text-accent" />
          <CardTitle className="text-sm">{group.pool}</CardTitle>
        </div>
        <span className="text-xs font-mono text-muted-foreground">
          {group.activeJobs}/{group.totalCapacity}
        </span>
      </CardHeader>

      <div className="mt-3 space-y-2">
        <div className="flex items-center justify-between text-xs">
          <span className="text-muted-foreground">Utilization</span>
          <span
            className={cn(
              "font-semibold",
              variant === "danger" && "text-danger",
              variant === "warning" && "text-warning",
              variant === "success" && "text-success",
            )}
          >
            {Math.round(group.utilization)}%
          </span>
        </div>
        <ProgressBar value={group.utilization} variant={variant} />
      </div>

      {group.workers.length > 0 && (
        <div className="mt-4 space-y-1.5">
          <p className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
            Workers
          </p>
          {group.workers.map((w) => (
            <div
              key={w.id}
              className="flex items-center justify-between text-xs"
            >
              <div className="flex items-center gap-1.5">
                {statusDot(w.status)}
                <span className="truncate text-ink">{w.name || w.id.slice(0, 8)}</span>
              </div>
              <span className="font-mono text-muted-foreground">
                {w.activeJobs}/{w.capacity}
              </span>
            </div>
          ))}
        </div>
      )}
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Grid
// ---------------------------------------------------------------------------

export function PoolUtilizationGrid() {
  const { data: workers, isLoading } = useWorkers();

  const pools = useMemo(() => {
    if (!workers || workers.length === 0) return [];

    const map = new Map<string, Worker[]>();
    for (const w of workers) {
      const pool = w.pool || "default";
      const list = map.get(pool) ?? [];
      list.push(w);
      map.set(pool, list);
    }

    return [...map.entries()]
      .map(([pool, members]): PoolGroup => {
        const activeJobs = members.reduce((sum, w) => sum + w.activeJobs, 0);
        const totalCapacity = members.reduce((sum, w) => sum + w.capacity, 0);
        const utilization = totalCapacity > 0 ? (activeJobs / totalCapacity) * 100 : 0;
        return { pool, workers: members, activeJobs, totalCapacity, utilization };
      })
      .sort((a, b) => b.utilization - a.utilization);
  }, [workers]);

  if (isLoading) {
    return (
      <div className="space-y-3">
        <h2 className="font-display text-lg font-semibold text-ink">Pool Utilization</h2>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {[1, 2, 3].map((i) => (
            <Card key={i} className="animate-pulse">
              <div className="space-y-3">
                <div className="h-4 w-1/2 rounded bg-surface2" />
                <div className="h-2 w-full rounded bg-surface2" />
                <div className="h-3 w-2/3 rounded bg-surface2" />
              </div>
            </Card>
          ))}
        </div>
      </div>
    );
  }

  if (pools.length === 0) {
    return (
      <div className="space-y-3">
        <h2 className="font-display text-lg font-semibold text-ink">Pool Utilization</h2>
        <p className="text-sm text-muted-foreground">No worker pools detected.</p>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <h2 className="font-display text-lg font-semibold text-ink">Pool Utilization</h2>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {pools.map((group) => (
          <PoolCard key={group.pool} group={group} />
        ))}
      </div>
    </div>
  );
}
