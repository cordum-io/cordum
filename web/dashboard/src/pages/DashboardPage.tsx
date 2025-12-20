import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import Card from "../components/Card";
import KpiCard from "../components/KpiCard";
import Loading from "../components/Loading";
import EmptyState from "../components/EmptyState";
import { fetchJobs, fetchWorkers } from "../lib/api";
import { useStreamStore } from "../state/streamStore";
import Badge from "../components/Badge";
import { formatUnixMillis } from "../lib/format";
import { Link } from "react-router-dom";
import { useInspectorStore } from "../state/inspectorStore";
import JsonViewer from "../components/JsonViewer";
import { useAuthStore } from "../state/authStore";

export default function DashboardPage() {
  const events = useStreamStore((s) => s.events);
  const showInspector = useInspectorStore((s) => s.show);
  const authStatus = useAuthStore((s) => s.status);
  const [filterTopic, setFilterTopic] = useState("");
  const [filterPool, setFilterPool] = useState("");
  const [filterJobId, setFilterJobId] = useState("");

  const canPoll = authStatus === "unknown" || authStatus === "authorized";

  const workersQ = useQuery({
    queryKey: ["workers", authStatus],
    queryFn: fetchWorkers,
    refetchInterval: canPoll ? 5_000 : false,
  });
  const jobsQ = useQuery({
    queryKey: ["jobs", { limit: 20, authStatus }],
    queryFn: () => fetchJobs({ limit: 20 }),
    refetchInterval: canPoll ? 3_000 : false,
  });

  const workers = workersQ.data ?? [];
  const jobs = jobsQ.data?.items ?? [];
  const workersCount = workers.length;
  const activeJobsCount = jobs.filter((j) =>
    ["PENDING", "SCHEDULED", "DISPATCHED", "RUNNING"].includes(j.state),
  ).length;
  const completedJobsCount = jobs.filter((j) => j.state === "SUCCEEDED").length;

  const filteredEvents = useMemo(() => {
    const topicQ = filterTopic.trim().toLowerCase();
    const poolQ = filterPool.trim().toLowerCase();
    const jobQ = filterJobId.trim().toLowerCase();
    if (!topicQ && !poolQ && !jobQ) {
      return events;
    }
    return events.filter((e) => {
      const p: any = e.packet;
      const topic = String(p?.jobRequest?.topic ?? "").toLowerCase();
      const pool = String(p?.heartbeat?.pool ?? "").toLowerCase();
      const jobId = String(p?.jobRequest?.jobId ?? p?.jobResult?.jobId ?? "").toLowerCase();
      if (topicQ && !topic.includes(topicQ)) return false;
      if (poolQ && !pool.includes(poolQ)) return false;
      if (jobQ && !jobId.includes(jobQ)) return false;
      return true;
    });
  }, [events, filterJobId, filterPool, filterTopic]);

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-4 gap-4">
        <KpiCard label="Workers Online" value={workersCount} />
        <KpiCard label="Active Jobs" value={activeJobsCount} />
        <KpiCard label="Completed Jobs" value={completedJobsCount} />
        <KpiCard label="Events (WS)" value={events.length} />
      </div>

      <div className="grid grid-cols-2 gap-6">
        <Card
          title="Live Feed"
          right={
            <div className="flex items-center gap-2">
              <button
                className="rounded-md border border-white/10 bg-black/20 px-2 py-1 text-xs text-zinc-300 hover:bg-black/30"
                onClick={() => useStreamStore.getState().clear()}
              >
                Clear
              </button>
            </div>
          }
        >
          <div className="mb-3 grid grid-cols-3 gap-2">
            <input
              value={filterTopic}
              onChange={(e) => setFilterTopic(e.target.value)}
              placeholder="topic contains…"
              className="rounded-lg border border-white/10 bg-black/20 px-3 py-1.5 text-xs text-zinc-200 placeholder:text-zinc-500"
            />
            <input
              value={filterPool}
              onChange={(e) => setFilterPool(e.target.value)}
              placeholder="pool contains…"
              className="rounded-lg border border-white/10 bg-black/20 px-3 py-1.5 text-xs text-zinc-200 placeholder:text-zinc-500"
            />
            <input
              value={filterJobId}
              onChange={(e) => setFilterJobId(e.target.value)}
              placeholder="job id contains…"
              className="rounded-lg border border-white/10 bg-black/20 px-3 py-1.5 text-xs text-zinc-200 placeholder:text-zinc-500"
            />
          </div>
          <div className="max-h-[420px] overflow-auto space-y-2">
            {filteredEvents.length === 0 ? (
              <EmptyState title="No events yet" description="Connect WS and run a job." />
            ) : (
              filteredEvents
                .slice(-100)
                .reverse()
                .map((e) => (
                  <button
                    key={e.received_at + e.summary}
                    type="button"
                    className="w-full rounded-lg border border-white/10 bg-black/20 px-3 py-2 text-left text-xs hover:bg-black/30"
                    onClick={() =>
                      showInspector(
                        `Event: ${e.kind}`,
                        <div className="space-y-3">
                          <div className="text-xs text-zinc-400">{e.summary}</div>
                          <JsonViewer value={e.packet} />
                        </div>,
                      )
                    }
                  >
                    <div className="flex items-center justify-between text-zinc-400">
                      <span>
                        {formatUnixMillis(e.received_at)} · {e.kind}
                      </span>
                      <span className="font-mono">{e.trace_id}</span>
                    </div>
                    <div className="mt-1 text-zinc-200">{e.summary}</div>
                  </button>
                ))
            )}
          </div>
        </Card>

        <div className="space-y-6">
          <Card title="Worker Mesh">
            {workersQ.isLoading ? (
              <Loading />
            ) : workersQ.isError ? (
              <EmptyState
                title={authStatus === "missing_api_key" || authStatus === "invalid_api_key" ? "Unauthorized" : "Failed to load workers"}
                description={
                  authStatus === "missing_api_key"
                    ? "Gateway requires an API key. Set it in Settings."
                    : authStatus === "invalid_api_key"
                      ? "API key was rejected. Update it in Settings."
                      : "Check API base/key in Settings."
                }
              />
            ) : workers.length === 0 ? (
              <EmptyState title="No workers" description="Start workers and watch heartbeats." />
            ) : (
              <div className="grid grid-cols-2 gap-2">
                {workers.slice(0, 6).map((w) => (
                  <button
                    key={w.worker_id}
                    type="button"
                    className="rounded-xl border border-white/10 bg-black/20 p-3 text-left text-xs hover:bg-black/30"
                    onClick={() =>
                      showInspector(
                        `Worker: ${w.worker_id}`,
                        <div className="space-y-3">
                          <div className="text-xs text-zinc-400">Snapshot</div>
                          <JsonViewer value={w} />
                        </div>,
                      )
                    }
                  >
                    <div className="truncate font-mono text-zinc-200">{w.worker_id}</div>
                    <div className="mt-1 text-zinc-500">
                      {w.pool || "-"} · {w.region || "local"}
                    </div>
                    <div className="mt-2 text-zinc-400">
                      jobs: {w.active_jobs ?? 0}/{w.max_parallel_jobs ?? "-"} · cpu:{" "}
                      {(w.cpu_load ?? 0).toFixed(1)}%
                    </div>
                  </button>
                ))}
              </div>
            )}
          </Card>

          <Card title="Recent Jobs">
            {jobsQ.isLoading ? (
              <Loading />
            ) : jobsQ.isError ? (
              <EmptyState
                title={authStatus === "missing_api_key" || authStatus === "invalid_api_key" ? "Unauthorized" : "Failed to load jobs"}
                description={
                  authStatus === "missing_api_key"
                    ? "Gateway requires an API key. Set it in Settings."
                    : authStatus === "invalid_api_key"
                      ? "API key was rejected. Update it in Settings."
                      : "Check API base/key in Settings."
                }
              />
            ) : (
              <div className="space-y-2">
                {jobs.map((j) => (
                  <Link
                    key={j.id}
                    to={`/jobs/${encodeURIComponent(j.id)}`}
                    className="flex items-center justify-between rounded-lg border border-white/10 bg-black/20 px-3 py-2 text-sm hover:bg-black/30"
                  >
                    <div className="min-w-0">
                      <div className="truncate font-mono text-xs text-zinc-300">{j.id}</div>
                      <div className="truncate text-xs text-zinc-500">{j.topic}</div>
                    </div>
                    <div className="flex items-center gap-2">
                      <Badge state={j.state} />
                    </div>
                  </Link>
                ))}
              </div>
            )}
          </Card>
        </div>
      </div>
    </div>
  );
}
