import { useMemo, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Area, AreaChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { api } from "../lib/api";
import { formatDateTime, formatPercent, formatRelative, formatShortDate } from "../lib/format";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Badge } from "../components/ui/Badge";
import { Select } from "../components/ui/Select";
import { Textarea } from "../components/ui/Textarea";
import { Input } from "../components/ui/Input";
import { ProgressBar } from "../components/ProgressBar";
import type { DLQEntry, Heartbeat } from "../types/api";

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
  const queryClient = useQueryClient();
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
  const configQuery = useQuery({
    queryKey: ["config", configScope, configScopeId],
    queryFn: () => api.getConfig(configScope, configScopeId),
  });

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

  const scrollToSection = (id: string) => {
    if (typeof document === "undefined") {
      return;
    }
    const node = document.getElementById(id);
    if (node) {
      node.scrollIntoView({ behavior: "smooth", block: "start" });
    }
  };

  const baseConfigText = useMemo(() => JSON.stringify(systemConfigQuery.data?.data || {}, null, 2), [systemConfigQuery.data]);
  const currentConfigText = useMemo(() => JSON.stringify(configQuery.data?.data || {}, null, 2), [configQuery.data]);
  const configDiff = useMemo(() => buildLineDiff(baseConfigText, currentConfigText), [baseConfigText, currentConfigText]);

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>System Health</CardTitle>
          <div className="text-xs text-muted">Gateway status snapshot</div>
        </CardHeader>
        <div className="grid gap-4 lg:grid-cols-3">
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
              <Button variant="outline" size="sm" type="button" onClick={() => scrollToSection("workers")}>
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
              <Button variant="outline" size="sm" type="button" onClick={() => scrollToSection("dlq")}>
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
              <Button variant="outline" size="sm" type="button" onClick={() => scrollToSection("pools")}>
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
            {dlqEntries.map((entry) => (
              <div key={entry.job_id} className="list-row">
                <div className="grid gap-3 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)_auto] lg:items-center">
                  <div>
                    <div className="text-sm font-semibold text-ink">Job {entry.job_id}</div>
                    <div className="text-xs text-muted">{entry.reason || entry.status}</div>
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
              </div>
            ))}
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
    </div>
  );
}
