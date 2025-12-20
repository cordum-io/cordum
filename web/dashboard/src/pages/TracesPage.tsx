import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import Card from "../components/Card";
import Loading from "../components/Loading";
import EmptyState from "../components/EmptyState";
import { fetchTrace } from "../lib/api";
import Badge from "../components/Badge";
import { Link, useSearchParams } from "react-router-dom";
import { useAuthStore } from "../state/authStore";

export default function TracesPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [traceID, setTraceID] = useState("");
  const [activeTrace, setActiveTrace] = useState("");
  const authStatus = useAuthStore((s) => s.status);

  useEffect(() => {
    const fromURL = searchParams.get("trace_id") || "";
    if (fromURL && fromURL !== activeTrace) {
      setTraceID(fromURL);
      setActiveTrace(fromURL);
    }
  }, [activeTrace, searchParams]);

  const q = useQuery({
    queryKey: ["trace", activeTrace, authStatus],
    queryFn: () => fetchTrace(activeTrace),
    enabled: !!activeTrace,
  });

  return (
    <div className="space-y-6">
      <Card
        title="Trace Lookup"
        right={
          <div className="flex items-center gap-2">
            <input
              value={traceID}
              onChange={(e) => setTraceID(e.target.value)}
              placeholder="trace id"
              className="w-[320px] rounded-lg border border-white/10 bg-black/20 px-3 py-1.5 text-xs text-zinc-200 placeholder:text-zinc-500"
            />
            <button
              onClick={() => {
                const next = traceID.trim();
                setActiveTrace(next);
                setSearchParams(next ? { trace_id: next } : {});
              }}
              className="rounded-lg border border-white/10 bg-black/20 px-3 py-1.5 text-xs text-zinc-200 hover:bg-black/30"
            >
              Load
            </button>
          </div>
        }
      >
        {!activeTrace ? (
          <EmptyState title="Enter a trace id" description="Use trace_id from job submission or logs." />
        ) : q.isLoading ? (
          <Loading label="Loading trace..." />
        ) : q.isError ? (
          <EmptyState
            title={authStatus === "missing_api_key" || authStatus === "invalid_api_key" ? "Unauthorized" : "Trace not found"}
            description={
              authStatus === "missing_api_key"
                ? "Gateway requires an API key. Set it in Settings."
                : authStatus === "invalid_api_key"
                  ? "API key was rejected. Update it in Settings."
                  : "Verify the trace id and API settings."
            }
          />
        ) : (q.data?.length ?? 0) === 0 ? (
          <EmptyState title="No jobs in trace" />
        ) : (
          <div className="space-y-2">
            {(q.data ?? []).map((j) => (
              <Link
                key={j.id}
                to={`/jobs/${encodeURIComponent(j.id)}`}
                className="flex items-center justify-between rounded-lg border border-white/10 bg-black/20 px-3 py-2 text-sm hover:bg-black/30"
              >
                <div className="min-w-0">
                  <div className="truncate font-mono text-xs text-zinc-300">{j.id}</div>
                  <div className="truncate text-xs text-zinc-500">{j.topic}</div>
                </div>
                <Badge state={j.state} />
              </Link>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}
