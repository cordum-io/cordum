import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery } from "@tanstack/react-query";
import Card from "../components/Card";
import Loading from "../components/Loading";
import EmptyState from "../components/EmptyState";
import Badge from "../components/Badge";
import { cancelJob, fetchJob } from "../lib/api";
import JsonViewer from "../components/JsonViewer";
import { useAuthStore } from "../state/authStore";
import { useStreamStore } from "../state/streamStore";
import { useMemo } from "react";
import { formatUnixMillis } from "../lib/format";
import { useInspectorStore } from "../state/inspectorStore";
import MemoryPointerViewer from "../components/MemoryPointerViewer";

export default function JobDetailPage() {
  const { id } = useParams();
  const authStatus = useAuthStore((s) => s.status);
  const canPoll = authStatus === "unknown" || authStatus === "authorized";
  const events = useStreamStore((s) => s.events);
  const showInspector = useInspectorStore((s) => s.show);

  const q = useQuery({
    queryKey: ["job", id, authStatus],
    queryFn: () => fetchJob(id!),
    enabled: !!id,
    refetchInterval: canPoll ? 2_000 : false,
  });

  const job = q.data;

  const jobEvents = useMemo(() => {
    if (!job) return [];
    const jobId = job.id;
    const traceId = job.trace_id;
    return events
      .filter((e) => {
        if (e.kind !== "jobRequest" && e.kind !== "jobResult") {
          return false;
        }
        const p: any = e.packet;
        const idFromPacket = String(p?.jobRequest?.jobId ?? p?.jobResult?.jobId ?? "");
        if (idFromPacket === jobId) {
          return true;
        }
        if (traceId && e.trace_id === traceId) {
          return true;
        }
        return false;
      })
      .slice(-50)
      .reverse();
  }, [events, job]);

  if (!id) {
    return <EmptyState title="Missing job id" />;
  }

  if (q.isLoading) {
    return <Loading label="Loading job..." />;
  }

  if (q.isError) {
    if (authStatus === "missing_api_key" || authStatus === "invalid_api_key") {
      return (
        <EmptyState
          title="Unauthorized"
          description={
            authStatus === "missing_api_key"
              ? "Gateway requires an API key. Set it in Settings."
              : "API key was rejected. Update it in Settings."
          }
        />
      );
    }
    return <EmptyState title="Job not found" description="Verify the job id and API settings." />;
  }
  
  if (!job) {
    return <EmptyState title="Job not found" description="Verify the job id and API settings." />;
  }

  const isTerminal =
    job.state === "SUCCEEDED" ||
    job.state === "FAILED" ||
    job.state === "CANCELLED" ||
    job.state === "TIMEOUT" ||
    job.state === "DENIED";
  const ctxPtr = job.context_ptr || `redis://ctx:${job.id}`;
  const resPtr = job.result_ptr || "";
  const resLabel = resPtr ? resPtr : isTerminal ? "no result pointer (job did not produce a result)" : "result not ready";
  const cancelM = useMutation({
    mutationFn: () => cancelJob(job.id),
    onSuccess: () => {
      void q.refetch();
    },
  });

  return (
    <div className="space-y-6">
      <Card
        title="Job"
        right={
          <div className="flex items-center gap-2">
            {!isTerminal ? (
              <button
                type="button"
                className="rounded-md border border-red-500/30 bg-red-500/10 px-2 py-1 text-xs text-red-200 hover:bg-red-500/20 disabled:opacity-50"
                disabled={cancelM.isPending}
                onClick={() => cancelM.mutate()}
                title="Cancel job"
              >
                Cancel
              </button>
            ) : null}
            <Badge state={job.state} />
          </div>
        }
      >
        <div className="grid grid-cols-2 gap-4 text-sm">
          <div>
            <div className="text-xs text-zinc-500">Job ID</div>
            <div className="mt-1 font-mono text-xs">{job.id}</div>
          </div>
          <div>
            <div className="text-xs text-zinc-500">Topic</div>
            <div className="mt-1">{job.topic || "-"}</div>
          </div>
          <div>
            <div className="text-xs text-zinc-500">Trace</div>
            <div className="mt-1">
              {job.trace_id ? (
                <Link
                  to={`/traces?trace_id=${encodeURIComponent(job.trace_id)}`}
                  className="font-mono text-xs text-zinc-200 hover:underline"
                >
                  {job.trace_id}
                </Link>
              ) : (
                <span className="text-zinc-500">-</span>
              )}
            </div>
          </div>
          <div>
            <div className="text-xs text-zinc-500">Tenant</div>
            <div className="mt-1">{job.tenant || "-"}</div>
          </div>
          <div>
            <div className="text-xs text-zinc-500">Safety</div>
            <div className="mt-1">
              {job.safety_decision || "-"} {job.safety_reason ? `(${job.safety_reason})` : ""}
            </div>
          </div>
          {job.error_message ? (
            <div className="col-span-2 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-200">
              <div className="text-xs text-red-200/80">Error</div>
              <div className="mt-1 whitespace-pre-wrap break-words font-mono text-xs">{job.error_message}</div>
            </div>
          ) : null}
          <div className="col-span-2">
            <div className="text-xs text-zinc-500">Context Pointer</div>
            <div className="mt-1 flex items-start justify-between gap-3">
              <div className="flex-1 break-all font-mono text-xs">{ctxPtr}</div>
              <button
                type="button"
                className="rounded-md border border-white/10 bg-black/20 px-2 py-1 text-xs text-zinc-300 hover:bg-black/30"
                onClick={() => showInspector("Memory: Context Pointer", <MemoryPointerViewer pointer={ctxPtr} />)}
              >
                View
              </button>
            </div>
          </div>
          <div className="col-span-2">
            <div className="text-xs text-zinc-500">Result Pointer</div>
            <div className="mt-1 flex items-start justify-between gap-3">
              <div className="flex-1 break-all font-mono text-xs">{resLabel}</div>
              <button
                type="button"
                disabled={!resPtr}
                className="rounded-md border border-white/10 bg-black/20 px-2 py-1 text-xs text-zinc-300 hover:bg-black/30 disabled:cursor-not-allowed disabled:opacity-50"
                onClick={() => showInspector("Memory: Result Pointer", <MemoryPointerViewer pointer={resPtr} />)}
              >
                View
              </button>
            </div>
          </div>
        </div>
      </Card>

      <Card title="Context">
        {job.context ? <JsonViewer value={job.context} /> : <EmptyState title="No context loaded" />}
      </Card>

      <Card title="Result">
        {job.result ? <JsonViewer value={job.result} /> : <EmptyState title="No result yet" />}
      </Card>

      <Card
        title="Events"
        right={
          <button
            className="rounded-md border border-white/10 bg-black/20 px-2 py-1 text-xs text-zinc-300 hover:bg-black/30"
            onClick={() => useStreamStore.getState().clear()}
          >
            Clear feed
          </button>
        }
      >
        {jobEvents.length === 0 ? (
          <EmptyState title="No matching WS events" description="Keep this tab open while running the job, or check the Dashboard Live Feed." />
        ) : (
          <div className="space-y-2">
            {jobEvents.map((e) => (
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
                <div className="flex items-center justify-between text-zinc-500">
                  <span>{formatUnixMillis(e.received_at)}</span>
                  <span className="font-mono">{e.kind}</span>
                </div>
                <div className="mt-1 text-zinc-200">{e.summary}</div>
              </button>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}
