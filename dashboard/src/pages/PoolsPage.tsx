import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { Activity, AlertTriangle, CheckCircle2, Server, Layers } from "lucide-react";
import { api } from "../lib/api";
import { formatPercent, formatRelative } from "../lib/format";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Badge } from "../components/ui/Badge";
import { ProgressBar } from "../components/ProgressBar";
import type { Heartbeat } from "../types/api";

const STALE_WORKER_MINUTES = 2;

type PoolSummary = {
  name: string;
  workers: Heartbeat[];
  topics: string[];
  requires: string[];
  avgCpu: number;
  avgMem: number;
  staleCount: number;
};

export function PoolsPage() {
  const [selectedPool, setSelectedPool] = useState<string | null>(null);

  const workersQuery = useQuery({
    queryKey: ["workers"],
    queryFn: () => api.listWorkers(),
    refetchInterval: 10_000,
  });
  const systemConfigQuery = useQuery({
    queryKey: ["config", "system", "default"],
    queryFn: () => api.getConfig("system", "default"),
  });

  const workers = useMemo(() => (workersQuery.data || []) as Heartbeat[], [workersQuery.data]);

  const cutoff = Date.now() - STALE_WORKER_MINUTES * 60 * 1000;
  const staleWorkers = useMemo(() => {
    return workers.filter((worker) => {
      if (!worker.updated_at) return false;
      const ts = new Date(worker.updated_at).getTime();
      return Number.isFinite(ts) && ts < cutoff;
    });
  }, [workers, cutoff]);

  const pools = useMemo<PoolSummary[]>(() => {
    const poolsConfig = (systemConfigQuery.data?.data?.pools || {}) as Record<string, unknown>;
    const poolDefs = (poolsConfig as { pools?: Record<string, { requires?: string[] }> }).pools || {};
    const topicsConfig = (poolsConfig as { topics?: Record<string, string | string[]> }).topics || {};

    // Build topic -> pool mapping
    const topicsByPool: Record<string, string[]> = {};
    for (const [topic, poolOrPools] of Object.entries(topicsConfig)) {
      const poolNames = Array.isArray(poolOrPools) ? poolOrPools : [poolOrPools];
      for (const poolName of poolNames) {
        if (!topicsByPool[poolName]) topicsByPool[poolName] = [];
        topicsByPool[poolName].push(topic);
      }
    }

    // Get all unique pool names
    const allPoolNames = new Set<string>([
      ...Object.keys(poolDefs),
      ...workers.map((w) => w.pool || "default"),
    ]);

    return Array.from(allPoolNames)
      .map((poolName) => {
        const poolWorkers = workers.filter((w) => (w.pool || "default") === poolName);
        const cpuValues = poolWorkers.map((w) => w.cpu_load).filter((v): v is number => typeof v === "number");
        const memValues = poolWorkers.map((w) => w.memory_load).filter((v): v is number => typeof v === "number");
        const avgCpu = cpuValues.length ? cpuValues.reduce((sum, v) => sum + v, 0) / cpuValues.length : 0;
        const avgMem = memValues.length ? memValues.reduce((sum, v) => sum + v, 0) / memValues.length : 0;
        const poolStale = poolWorkers.filter((w) => {
          if (!w.updated_at) return false;
          const ts = new Date(w.updated_at).getTime();
          return Number.isFinite(ts) && ts < cutoff;
        });

        return {
          name: poolName,
          workers: poolWorkers,
          topics: topicsByPool[poolName] || [],
          requires: poolDefs[poolName]?.requires || [],
          avgCpu,
          avgMem,
          staleCount: poolStale.length,
        };
      })
      .sort((a, b) => b.workers.length - a.workers.length);
  }, [systemConfigQuery.data, workers, cutoff]);

  const selectedPoolData = selectedPool ? pools.find((p) => p.name === selectedPool) : null;

  const totalWorkers = workers.length;
  const healthyWorkers = totalWorkers - staleWorkers.length;
  const totalTopics = pools.reduce((sum, p) => sum + p.topics.length, 0);

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Pools & Workers</CardTitle>
          <div className="text-xs text-muted">Worker pools, topic routing, and health monitoring</div>
        </CardHeader>
        <div className="grid gap-4 lg:grid-cols-4">
          <div className="rounded-2xl border border-border bg-white/70 p-4">
            <div className="flex items-center gap-2 text-xs uppercase tracking-[0.2em] text-muted">
              <Layers className="h-4 w-4" />
              Pools
            </div>
            <div className="mt-2 text-2xl font-semibold text-ink">{pools.length}</div>
          </div>
          <div className="rounded-2xl border border-border bg-white/70 p-4">
            <div className="flex items-center gap-2 text-xs uppercase tracking-[0.2em] text-muted">
              <Server className="h-4 w-4" />
              Workers
            </div>
            <div className="mt-2 text-2xl font-semibold text-ink">{totalWorkers}</div>
            <div className="text-xs text-muted">{healthyWorkers} healthy</div>
          </div>
          <div className="rounded-2xl border border-border bg-white/70 p-4">
            <div className="flex items-center gap-2 text-xs uppercase tracking-[0.2em] text-muted">
              <Activity className="h-4 w-4" />
              Topics Mapped
            </div>
            <div className="mt-2 text-2xl font-semibold text-ink">{totalTopics}</div>
          </div>
          <div className="rounded-2xl border border-border bg-white/70 p-4">
            <div className="flex items-center gap-2 text-xs uppercase tracking-[0.2em] text-muted">
              {staleWorkers.length > 0 ? (
                <AlertTriangle className="h-4 w-4 text-warning" />
              ) : (
                <CheckCircle2 className="h-4 w-4 text-success" />
              )}
              Health
            </div>
            <div className="mt-2 text-2xl font-semibold text-ink">
              {staleWorkers.length > 0 ? (
                <span className="text-warning">{staleWorkers.length} stale</span>
              ) : (
                <span className="text-success">Healthy</span>
              )}
            </div>
            <div className="text-xs text-muted">Heartbeat &gt; {STALE_WORKER_MINUTES}m = stale</div>
          </div>
        </div>
      </Card>

      <div className="grid gap-6 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Pool Overview</CardTitle>
            <div className="text-xs text-muted">Click a pool to see workers</div>
          </CardHeader>
          {pools.length === 0 ? (
            <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
              No pools configured or no workers registered.
            </div>
          ) : (
            <div className="space-y-3">
              {pools.map((pool) => {
                const isSelected = selectedPool === pool.name;
                const isOverloaded = pool.avgCpu >= 80 || pool.avgMem >= 80;
                const hasStale = pool.staleCount > 0;
                return (
                  <button
                    key={pool.name}
                    type="button"
                    onClick={() => setSelectedPool(isSelected ? null : pool.name)}
                    className={`w-full text-left rounded-2xl border p-4 transition ${
                      isSelected
                        ? "border-accent bg-accent/5"
                        : "border-border bg-white/70 hover:border-accent/50"
                    }`}
                  >
                    <div className="flex items-center justify-between">
                      <div>
                        <div className="flex items-center gap-2">
                          <span className="text-sm font-semibold text-ink">{pool.name}</span>
                          {isOverloaded ? (
                            <span className="rounded bg-warning/10 px-1.5 py-0.5 text-[10px] font-medium text-warning">
                              high load
                            </span>
                          ) : null}
                          {hasStale ? (
                            <span className="rounded bg-danger/10 px-1.5 py-0.5 text-[10px] font-medium text-danger">
                              {pool.staleCount} stale
                            </span>
                          ) : null}
                        </div>
                        <div className="mt-1 text-xs text-muted">
                          {pool.workers.length} workers · {pool.topics.length} topics
                        </div>
                      </div>
                      <div className="flex items-center gap-2">
                        {pool.requires.length > 0 ? (
                          <Badge variant="info">{pool.requires.join(", ")}</Badge>
                        ) : (
                          <Badge variant="default">general</Badge>
                        )}
                      </div>
                    </div>
                    <div className="mt-3 grid gap-2 lg:grid-cols-2">
                      <div>
                        <div className="mb-1 flex items-center justify-between text-xs text-muted">
                          <span>CPU</span>
                          <span>{formatPercent(pool.avgCpu)}</span>
                        </div>
                        <ProgressBar value={pool.avgCpu} variant={pool.avgCpu >= 80 ? "danger" : "default"} />
                      </div>
                      <div>
                        <div className="mb-1 flex items-center justify-between text-xs text-muted">
                          <span>Memory</span>
                          <span>{formatPercent(pool.avgMem)}</span>
                        </div>
                        <ProgressBar value={pool.avgMem} variant={pool.avgMem >= 80 ? "danger" : "default"} />
                      </div>
                    </div>
                    {pool.topics.length > 0 ? (
                      <div className="mt-3 flex flex-wrap gap-1">
                        {pool.topics.slice(0, 5).map((topic) => (
                          <span
                            key={topic}
                            className="rounded bg-muted/10 px-1.5 py-0.5 text-[10px] font-mono text-muted"
                          >
                            {topic}
                          </span>
                        ))}
                        {pool.topics.length > 5 ? (
                          <span className="text-[10px] text-muted">+{pool.topics.length - 5} more</span>
                        ) : null}
                      </div>
                    ) : (
                      <div className="mt-3 text-xs text-muted italic">No topics mapped to this pool</div>
                    )}
                  </button>
                );
              })}
            </div>
          )}
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>
              {selectedPoolData ? `Workers in ${selectedPoolData.name}` : "Workers"}
            </CardTitle>
            {selectedPoolData ? (
              <Button variant="ghost" size="sm" type="button" onClick={() => setSelectedPool(null)}>
                Clear
              </Button>
            ) : null}
          </CardHeader>
          {(() => {
            const displayWorkers = selectedPoolData?.workers || workers;
            if (displayWorkers.length === 0) {
              return (
                <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
                  {selectedPoolData ? "No workers in this pool." : "No workers registered."}
                </div>
              );
            }
            return (
              <div className="space-y-2 max-h-[500px] overflow-y-auto">
                {displayWorkers.map((worker, index) => {
                  const isStale = (() => {
                    if (!worker.updated_at) return false;
                    const ts = new Date(worker.updated_at).getTime();
                    return Number.isFinite(ts) && ts < cutoff;
                  })();
                  return (
                    <div
                      key={`${worker.worker_id}-${index}`}
                      className={`rounded-xl border p-3 ${
                        isStale ? "border-warning/50 bg-warning/5" : "border-border bg-white/70"
                      }`}
                    >
                      <div className="flex items-center justify-between">
                        <div className="text-xs font-semibold text-ink truncate max-w-[150px]" title={worker.worker_id}>
                          {worker.worker_id || "worker"}
                        </div>
                        <div className="flex items-center gap-1">
                          {isStale ? (
                            <span className="rounded bg-warning/10 px-1.5 py-0.5 text-[9px] font-medium text-warning">
                              stale
                            </span>
                          ) : (
                            <span className="rounded bg-success/10 px-1.5 py-0.5 text-[9px] font-medium text-success">
                              active
                            </span>
                          )}
                          <span className="rounded bg-accent/10 px-1.5 py-0.5 text-[9px] font-medium text-accent">
                            {worker.pool || "default"}
                          </span>
                        </div>
                      </div>
                      <div className="mt-2 grid gap-1 text-[10px] text-muted">
                        <div className="flex justify-between">
                          <span>CPU</span>
                          <span>{typeof worker.cpu_load === "number" ? formatPercent(worker.cpu_load) : "-"}</span>
                        </div>
                        <div className="flex justify-between">
                          <span>Memory</span>
                          <span>{typeof worker.memory_load === "number" ? formatPercent(worker.memory_load) : "-"}</span>
                        </div>
                        <div className="flex justify-between">
                          <span>Last seen</span>
                          <span>{worker.updated_at ? formatRelative(worker.updated_at) : "-"}</span>
                        </div>
                      </div>
                    </div>
                  );
                })}
              </div>
            );
          })()}
        </Card>
      </div>

      {staleWorkers.length > 0 ? (
        <Card>
          <CardHeader>
            <CardTitle>Stale Workers</CardTitle>
            <div className="text-xs text-warning">Workers with heartbeat older than {STALE_WORKER_MINUTES} minutes</div>
          </CardHeader>
          <div className="grid gap-3 lg:grid-cols-3">
            {staleWorkers.map((worker, index) => (
              <div
                key={`stale-${worker.worker_id}-${index}`}
                className="rounded-2xl border border-warning/50 bg-warning/5 p-4"
              >
                <div className="flex items-center justify-between">
                  <div className="text-sm font-semibold text-ink">{worker.worker_id || "worker"}</div>
                  <Badge variant="warning">{worker.pool || "default"}</Badge>
                </div>
                <div className="mt-2 text-xs text-muted">
                  Last heartbeat: {worker.updated_at ? formatRelative(worker.updated_at) : "unknown"}
                </div>
              </div>
            ))}
          </div>
        </Card>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle>Topic Routing</CardTitle>
          <div className="text-xs text-muted">How job topics are mapped to worker pools</div>
        </CardHeader>
        {pools.filter((p) => p.topics.length > 0).length === 0 ? (
          <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
            No topic mappings configured. Jobs will use the default pool.
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-border text-left">
                  <th className="pb-2 font-semibold uppercase tracking-[0.2em] text-muted">Topic</th>
                  <th className="pb-2 font-semibold uppercase tracking-[0.2em] text-muted">Pool</th>
                  <th className="pb-2 font-semibold uppercase tracking-[0.2em] text-muted">Workers</th>
                  <th className="pb-2 font-semibold uppercase tracking-[0.2em] text-muted">Requires</th>
                </tr>
              </thead>
              <tbody>
                {pools
                  .flatMap((pool) =>
                    pool.topics.map((topic) => ({
                      topic,
                      pool: pool.name,
                      workers: pool.workers.length,
                      requires: pool.requires,
                    }))
                  )
                  .sort((a, b) => a.topic.localeCompare(b.topic))
                  .map(({ topic, pool, workers: workerCount, requires }) => (
                    <tr key={`${topic}-${pool}`} className="border-b border-border/50">
                      <td className="py-2 font-mono text-ink">{topic}</td>
                      <td className="py-2">
                        <button
                          type="button"
                          onClick={() => setSelectedPool(pool)}
                          className="text-accent hover:underline"
                        >
                          {pool}
                        </button>
                      </td>
                      <td className="py-2 text-muted">{workerCount}</td>
                      <td className="py-2">
                        {requires.length > 0 ? (
                          <span className="rounded bg-info/10 px-1.5 py-0.5 text-[10px] text-info">
                            {requires.join(", ")}
                          </span>
                        ) : (
                          <span className="text-muted">-</span>
                        )}
                      </td>
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
