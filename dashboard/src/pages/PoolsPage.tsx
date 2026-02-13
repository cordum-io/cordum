import { useMemo, useState } from "react";
import { Server, ChevronDown, ChevronRight, AlertTriangle } from "lucide-react";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Badge } from "../components/ui/Badge";
import { ProgressBar } from "../components/ProgressBar";
import { useWorkers } from "../hooks/useWorkers";
import { cn } from "../lib/utils";
import type { Worker } from "../api/types";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface PoolGroup {
  pool: string;
  workers: Worker[];
  online: number;
  offline: number;
  draining: number;
  activeJobs: number;
  totalCapacity: number;
  utilization: number;
  health: "healthy" | "degraded" | "critical";
}

type HealthFilter = "all" | "healthy" | "degraded" | "critical";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function computeHealth(utilization: number): PoolGroup["health"] {
  if (utilization > 95) return "critical";
  if (utilization >= 80) return "degraded";
  return "healthy";
}

function utilizationVariant(pct: number): "success" | "warning" | "danger" {
  if (pct > 80) return "danger";
  if (pct >= 50) return "warning";
  return "success";
}

function healthBadgeVariant(health: PoolGroup["health"]) {
  switch (health) {
    case "critical":
      return "danger" as const;
    case "degraded":
      return "warning" as const;
    case "healthy":
      return "success" as const;
  }
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

function relativeTime(iso?: string): string {
  if (!iso) return "—";
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 0 || isNaN(diff)) return "—";
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

// ---------------------------------------------------------------------------
// Pool card
// ---------------------------------------------------------------------------

function PoolCard({
  group,
  expanded,
  onToggle,
}: {
  group: PoolGroup;
  expanded: boolean;
  onToggle: () => void;
}) {
  const variant = utilizationVariant(group.utilization);
  const Chevron = expanded ? ChevronDown : ChevronRight;

  return (
    <Card className="col-span-1">
      <button
        type="button"
        className="w-full text-left"
        onClick={onToggle}
      >
        <CardHeader>
          <div className="flex items-center gap-2">
            <Server className="h-4 w-4 text-accent" />
            <CardTitle className="text-sm">{group.pool}</CardTitle>
            <Badge variant={healthBadgeVariant(group.health)} className="ml-1 text-[10px] px-2 py-0.5">
              {group.health}
            </Badge>
          </div>
          <Chevron className="h-4 w-4 text-muted" />
        </CardHeader>

        {/* Stats row */}
        <div className="flex flex-wrap gap-3 text-xs text-muted">
          <span>
            <span className="font-semibold text-ink">{group.workers.length}</span> workers
          </span>
          <span>
            <span className="font-semibold text-success">{group.online}</span> online
          </span>
          {group.draining > 0 && (
            <span>
              <span className="font-semibold text-warning">{group.draining}</span> draining
            </span>
          )}
          {group.offline > 0 && (
            <span>
              <span className="font-semibold text-muted">{group.offline}</span> offline
            </span>
          )}
        </div>

        {/* Utilization bar */}
        <div className="mt-3 space-y-1">
          <div className="flex items-center justify-between text-xs">
            <span className="text-muted">Utilization</span>
            <span className="font-mono text-muted">
              {group.activeJobs}/{group.totalCapacity}
            </span>
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
      </button>

      {/* Expanded worker list */}
      {expanded && group.workers.length > 0 && (
        <div className="mt-4 border-t border-border pt-3">
          <p className="mb-2 text-[10px] font-semibold uppercase tracking-widest text-muted">
            Workers in {group.pool}
          </p>
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-border text-left text-[10px] uppercase tracking-widest text-muted">
                  <th className="pb-1.5 pr-3">Worker</th>
                  <th className="pb-1.5 pr-3">Status</th>
                  <th className="pb-1.5 pr-3">Active/Cap</th>
                  <th className="pb-1.5 pr-3">Heartbeat</th>
                </tr>
              </thead>
              <tbody>
                {group.workers.map((w) => (
                  <tr key={w.id} className="border-b border-border/50">
                    <td className="py-1.5 pr-3">
                      <div className="flex items-center gap-1.5">
                        {statusDot(w.status)}
                        <span className="truncate font-medium text-ink">
                          {w.name || w.id.slice(0, 12)}
                        </span>
                      </div>
                    </td>
                    <td className="py-1.5 pr-3">
                      <Badge
                        variant={
                          w.status === "online" || w.status === "active"
                            ? "success"
                            : w.status === "draining"
                              ? "warning"
                              : "default"
                        }
                        className="text-[10px] px-2 py-0.5"
                      >
                        {w.status}
                      </Badge>
                    </td>
                    <td className="py-1.5 pr-3 font-mono text-muted">
                      {w.activeJobs}/{w.capacity}
                    </td>
                    <td className="py-1.5 pr-3 text-muted">
                      {relativeTime(w.lastHeartbeat)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

export default function PoolsPage() {
  const { data: workers, isLoading, isError } = useWorkers();
  const [selectedPool, setSelectedPool] = useState<string | null>(null);
  const [healthFilter, setHealthFilter] = useState<HealthFilter>("all");

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
        const online = members.filter(
          (w) => w.status === "online" || w.status === "active",
        ).length;
        const draining = members.filter((w) => w.status === "draining").length;
        const offline = members.length - online - draining;
        return {
          pool,
          workers: members,
          online,
          offline,
          draining,
          activeJobs,
          totalCapacity,
          utilization,
          health: computeHealth(utilization),
        };
      })
      .sort((a, b) => b.utilization - a.utilization);
  }, [workers]);

  const filtered = useMemo(
    () =>
      healthFilter === "all"
        ? pools
        : pools.filter((p) => p.health === healthFilter),
    [pools, healthFilter],
  );

  // -------------------------------------------------------------------------
  // Loading
  // -------------------------------------------------------------------------

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <h1 className="font-display text-2xl font-semibold text-ink">Worker Pools</h1>
        </div>
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

  // -------------------------------------------------------------------------
  // Error
  // -------------------------------------------------------------------------

  if (isError) {
    return (
      <div className="space-y-6">
        <h1 className="font-display text-2xl font-semibold text-ink">Worker Pools</h1>
        <Card className="flex items-center gap-3 text-sm text-danger">
          <AlertTriangle className="h-5 w-5" />
          Failed to load worker pools. Please try again.
        </Card>
      </div>
    );
  }

  // -------------------------------------------------------------------------
  // Empty
  // -------------------------------------------------------------------------

  if (pools.length === 0) {
    return (
      <div className="space-y-6">
        <h1 className="font-display text-2xl font-semibold text-ink">Worker Pools</h1>
        <Card>
          <p className="text-sm text-muted">No worker pools detected.</p>
        </Card>
      </div>
    );
  }

  // -------------------------------------------------------------------------
  // Main
  // -------------------------------------------------------------------------

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="font-display text-2xl font-semibold text-ink">Worker Pools</h1>
        <div className="flex items-center gap-2">
          <span className="text-xs text-muted">Health:</span>
          {(["all", "healthy", "degraded", "critical"] as const).map((h) => (
            <button
              key={h}
              type="button"
              onClick={() => setHealthFilter(h)}
              className={cn(
                "rounded-full px-3 py-1 text-xs font-semibold capitalize transition",
                healthFilter === h
                  ? "bg-accent/15 text-accent"
                  : "text-muted hover:bg-surface2",
              )}
            >
              {h}
            </button>
          ))}
        </div>
      </div>

      {filtered.length === 0 ? (
        <Card>
          <p className="text-sm text-muted">
            No pools match the &ldquo;{healthFilter}&rdquo; filter.
          </p>
        </Card>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {filtered.map((group) => (
            <PoolCard
              key={group.pool}
              group={group}
              expanded={selectedPool === group.pool}
              onToggle={() =>
                setSelectedPool((prev) =>
                  prev === group.pool ? null : group.pool,
                )
              }
            />
          ))}
        </div>
      )}
    </div>
  );
}
