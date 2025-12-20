import { useQuery } from "@tanstack/react-query";
import Card from "../components/Card";
import DataTable, { type Column } from "../components/DataTable";
import Loading from "../components/Loading";
import EmptyState from "../components/EmptyState";
import { fetchWorkers, type WorkerHeartbeat } from "../lib/api";
import { useAuthStore } from "../state/authStore";
import { useInspectorStore } from "../state/inspectorStore";
import JsonViewer from "../components/JsonViewer";
import { useStreamStore } from "../state/streamStore";
import { formatUnixMillis } from "../lib/format";

export default function WorkersPage() {
  const authStatus = useAuthStore((s) => s.status);
  const canPoll = authStatus === "unknown" || authStatus === "authorized";
  const showInspector = useInspectorStore((s) => s.show);
  const events = useStreamStore((s) => s.events);

  const q = useQuery({
    queryKey: ["workers", authStatus],
    queryFn: fetchWorkers,
    refetchInterval: canPoll ? 5_000 : false,
  });

  const columns: Column<WorkerHeartbeat>[] = [
    {
      key: "worker_id",
      header: "Worker",
      className: "w-[260px] font-mono text-xs",
      render: (w) => w.worker_id,
    },
    { key: "pool", header: "Pool", render: (w) => w.pool || "-" },
    { key: "region", header: "Region", render: (w) => w.region || "-" },
    {
      key: "load",
      header: "Load",
      render: (w) =>
        `${(w.cpu_load ?? 0).toFixed(1)}% / ${(w.gpu_utilization ?? 0).toFixed(1)}%`,
    },
    {
      key: "jobs",
      header: "Jobs",
      render: (w) => `${w.active_jobs ?? 0} / ${w.max_parallel_jobs ?? "-"}`,
    },
  ];

  return (
    <div className="space-y-6">
      <Card title="Workers">
        {q.isLoading ? (
          <Loading />
        ) : q.isError ? (
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
        ) : (
          <DataTable
            columns={columns}
            rows={q.data ?? []}
            rowKey={(w) => w.worker_id}
            onRowClick={(w) => {
              const recent = events
                .filter((e) => {
                  const p: any = e.packet;
                  const hbWorker = String(p?.heartbeat?.workerId ?? "");
                  const jrWorker = String(p?.jobResult?.workerId ?? "");
                  return hbWorker === w.worker_id || jrWorker === w.worker_id;
                })
                .slice(-30)
                .reverse();

              showInspector(
                `Worker: ${w.worker_id}`,
                <div className="space-y-3">
                  <div className="text-xs text-zinc-400">Snapshot</div>
                  <JsonViewer value={w} />
                  <div className="text-xs text-zinc-400">Recent events</div>
                  {recent.length === 0 ? (
                    <div className="text-xs text-zinc-500">No recent events for this worker.</div>
                  ) : (
                    <div className="max-h-[260px] space-y-2 overflow-auto">
                      {recent.map((e) => (
                        <div
                          key={e.received_at + e.summary}
                          className="rounded-lg border border-white/10 bg-black/20 px-3 py-2 text-xs"
                        >
                          <div className="flex items-center justify-between text-zinc-500">
                            <span>{formatUnixMillis(e.received_at)}</span>
                            <span className="font-mono">{e.kind}</span>
                          </div>
                          <div className="mt-1 text-zinc-200">{e.summary}</div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>,
              );
            }}
          />
        )}
      </Card>
    </div>
  );
}
