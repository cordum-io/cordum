import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import Card from "../components/Card";
import Loading from "../components/Loading";
import EmptyState from "../components/EmptyState";
import DataTable, { type Column } from "../components/DataTable";
import Badge from "../components/Badge";
import { fetchJobs, type JobRecord } from "../lib/api";
import { formatUnixSeconds } from "../lib/format";
import { useAuthStore } from "../state/authStore";
import { useInspectorStore } from "../state/inspectorStore";
import JsonViewer from "../components/JsonViewer";
import MemoryPointerViewer from "../components/MemoryPointerViewer";

function isTerminalJobState(state: JobRecord["state"]): boolean {
  return state === "SUCCEEDED" || state === "FAILED" || state === "CANCELLED" || state === "TIMEOUT" || state === "DENIED";
}

function hasResultPointer(state: JobRecord["state"]): boolean {
  return state === "SUCCEEDED";
}

export default function JobsPage() {
  const [state, setState] = useState("");
  const [topic, setTopic] = useState("");
  const [jobID, setJobID] = useState("");
  const authStatus = useAuthStore((s) => s.status);
  const canPoll = authStatus === "unknown" || authStatus === "authorized";
  const showInspector = useInspectorStore((s) => s.show);

  const q = useQuery({
    queryKey: ["jobs", { state, topic, limit: 50, authStatus }],
    queryFn: () => fetchJobs({ state: state || undefined, topic: topic || undefined, limit: 50 }),
    refetchInterval: canPoll ? 3_000 : false,
  });

  const columns: Column<JobRecord>[] = useMemo(
    () => [
      {
        key: "id",
        header: "Job ID",
        className: "w-[420px] font-mono text-xs",
        render: (j) => (
          <div className="space-y-1">
            <Link to={`/jobs/${encodeURIComponent(j.id)}`} className="hover:underline">
              {j.id}
            </Link>
            <div className="space-y-1 text-[11px] text-zinc-500">
              <div className="flex items-center gap-2">
                <span className="w-8 shrink-0">ctx</span>
                <span title={`redis://ctx:${j.id}`} className="min-w-0 flex-1 truncate font-mono text-zinc-300">
                  {`redis://ctx:${j.id}`}
                </span>
                <button
                  type="button"
                  className="shrink-0 rounded-md border border-white/10 bg-black/20 px-2 py-1 text-[10px] text-zinc-300 hover:bg-black/30"
                  onClick={() =>
                    showInspector(
                      "Memory: Context Pointer",
                      <MemoryPointerViewer pointer={`redis://ctx:${j.id}`} />,
                    )
                  }
                >
                  View
                </button>
              </div>
              <div className="flex items-center gap-2">
                <span className="w-8 shrink-0">res</span>
                {hasResultPointer(j.state) ? (
                  <span title={`redis://res:${j.id}`} className="min-w-0 flex-1 truncate font-mono text-zinc-300">
                    {`redis://res:${j.id}`}
                  </span>
                ) : (
                  <span className="min-w-0 flex-1 truncate font-mono text-zinc-500">
                    {isTerminalJobState(j.state) ? "no result pointer" : "result not ready"}
                  </span>
                )}
                <button
                  type="button"
                  disabled={!hasResultPointer(j.state)}
                  className="shrink-0 rounded-md border border-white/10 bg-black/20 px-2 py-1 text-[10px] text-zinc-300 hover:bg-black/30 disabled:cursor-not-allowed disabled:opacity-50"
                  onClick={() =>
                    showInspector(
                      "Memory: Result Pointer",
                      <MemoryPointerViewer pointer={`redis://res:${j.id}`} />,
                    )
                  }
                >
                  View
                </button>
              </div>
            </div>
          </div>
        ),
      },
      { key: "topic", header: "Topic", render: (j) => j.topic || "-" },
      {
        key: "trace_id",
        header: "Trace",
        className: "w-[260px] font-mono text-xs",
        render: (j) =>
          j.trace_id ? (
            <Link to={`/traces?trace_id=${encodeURIComponent(j.trace_id)}`} className="hover:underline">
              {j.trace_id}
            </Link>
          ) : (
            <span className="text-zinc-500">-</span>
          ),
      },
      { key: "state", header: "State", render: (j) => <Badge state={j.state} /> },
      { key: "updated", header: "Updated", render: (j) => formatUnixSeconds(j.updated_at) },
    ],
    [showInspector],
  );

  const filteredRows = useMemo(() => {
    const items = q.data?.items ?? [];
    const qid = jobID.trim().toLowerCase();
    if (!qid) {
      return items;
    }
    return items.filter((j) => j.id.toLowerCase().includes(qid));
  }, [jobID, q.data?.items]);

  return (
    <div className="space-y-6">
      <Card
        title="Jobs"
        right={
          <div className="flex items-center gap-2">
            <input
              value={jobID}
              onChange={(e) => setJobID(e.target.value)}
              placeholder="job id contains..."
              className="w-[220px] rounded-lg border border-white/10 bg-black/20 px-3 py-1.5 text-xs text-zinc-200 placeholder:text-zinc-500"
            />
            <input
              value={topic}
              onChange={(e) => setTopic(e.target.value)}
              placeholder="topic (e.g. job.echo)"
              className="w-[220px] rounded-lg border border-white/10 bg-black/20 px-3 py-1.5 text-xs text-zinc-200 placeholder:text-zinc-500"
            />
            <select
              value={state}
              onChange={(e) => setState(e.target.value)}
              className="rounded-lg border border-white/10 bg-black/20 px-2 py-1.5 text-xs text-zinc-200"
            >
              <option value="">all states</option>
              {[
                "PENDING",
                "SCHEDULED",
                "DISPATCHED",
                "RUNNING",
                "SUCCEEDED",
                "FAILED",
                "CANCELLED",
                "TIMEOUT",
                "DENIED",
              ].map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </div>
        }
      >
        {q.isLoading ? (
          <Loading />
        ) : q.isError ? (
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
        ) : (q.data?.items?.length ?? 0) === 0 ? (
          <EmptyState title="No jobs" description="Submit a job from Chat or run a worker." />
        ) : (
          <DataTable
            columns={columns}
            rows={filteredRows}
            rowKey={(j) => j.id}
            onRowClick={(j) =>
              showInspector(
                `Job: ${j.id}`,
                <div className="space-y-3">
                  <div className="text-xs text-zinc-400">Pointers</div>
                  <div className="space-y-2 text-xs">
                    <div className="flex items-center justify-between gap-2 rounded-lg border border-white/10 bg-black/20 px-3 py-2">
                      <div className="min-w-0">
                        <div className="text-[11px] text-zinc-500">Context</div>
                        <div className="truncate font-mono text-[11px] text-zinc-200">{`redis://ctx:${j.id}`}</div>
                      </div>
                      <button
                        type="button"
                        className="shrink-0 rounded-md border border-white/10 bg-black/20 px-2 py-1 text-[10px] text-zinc-300 hover:bg-black/30"
                        onClick={() =>
                          showInspector("Memory: Context Pointer", <MemoryPointerViewer pointer={`redis://ctx:${j.id}`} />)
                        }
                      >
                        View
                      </button>
                    </div>
                    <div className="flex items-center justify-between gap-2 rounded-lg border border-white/10 bg-black/20 px-3 py-2">
                      <div className="min-w-0">
                        <div className="text-[11px] text-zinc-500">Result</div>
                        <div className="truncate font-mono text-[11px] text-zinc-200">
                          {hasResultPointer(j.state) ? `redis://res:${j.id}` : isTerminalJobState(j.state) ? "no result pointer" : "result not ready"}
                        </div>
                      </div>
                      <button
                        type="button"
                        disabled={!hasResultPointer(j.state)}
                        className="shrink-0 rounded-md border border-white/10 bg-black/20 px-2 py-1 text-[10px] text-zinc-300 hover:bg-black/30 disabled:cursor-not-allowed disabled:opacity-50"
                        onClick={() =>
                          showInspector("Memory: Result Pointer", <MemoryPointerViewer pointer={`redis://res:${j.id}`} />)
                        }
                      >
                        View
                      </button>
                    </div>
                  </div>
                  <div className="text-xs text-zinc-400">Record</div>
                  <JsonViewer value={j} />
                  <div className="text-xs text-zinc-500">Tip: click the Job ID link for full detail.</div>
                </div>,
              )
            }
          />
        )}
      </Card>
    </div>
  );
}
