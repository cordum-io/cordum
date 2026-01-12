import { useMemo, useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { formatDateTime, formatRelative } from "../lib/format";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Badge } from "../components/ui/Badge";
import { Input } from "../components/ui/Input";
import { Button } from "../components/ui/Button";
import { JobStatusBadge } from "../components/StatusBadge";
import type { JobStatus } from "../types/api";

type TraceJob = {
  job_id: string;
  topic: string;
  state: JobStatus;
  created_at?: string;
  updated_at?: string;
  tenant?: string;
  parent_job_id?: string;
  error_message?: string;
};

export function TracePage() {
  const { id } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const [inputId, setInputId] = useState(id || "");
  
  const traceId = id || searchParams.get("id");

  const query = useQuery({
    queryKey: ["trace", traceId],
    queryFn: () => api.getTrace(traceId as string),
    enabled: Boolean(traceId),
  });

  // The API returns a list of JobRecord objects.
  // We need to sort them by creation time and ideally build a tree.
  const jobs = useMemo(() => {
    const raw = (query.data || []) as unknown as TraceJob[];
    return raw.sort((a, b) => {
      const tA = new Date(a.created_at || 0).getTime();
      const tB = new Date(b.created_at || 0).getTime();
      return tA - tB;
    });
  }, [query.data]);

  const rootJob = jobs.find((j) => !j.parent_job_id);
  const startTime = rootJob ? new Date(rootJob.created_at || 0).getTime() : 0;

  const handleSearch = () => {
    if (inputId.trim()) {
      setSearchParams({ id: inputId.trim() });
    }
  };

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Trace Explorer</CardTitle>
          <div className="text-xs text-muted">Visualize distributed job execution traces</div>
        </CardHeader>
        <div className="flex gap-2 max-w-lg">
          <Input 
            value={inputId} 
            onChange={(e) => setInputId(e.target.value)} 
            placeholder="Enter Trace ID..." 
          />
          <Button onClick={handleSearch} variant="primary" disabled={!inputId}>
            Load
          </Button>
        </div>
      </Card>

      {query.isLoading ? (
        <div className="text-sm text-muted">Loading trace...</div>
      ) : null}

      {query.isError ? (
        <div className="text-sm text-danger">Failed to load trace. It may not exist.</div>
      ) : null}

      {jobs.length > 0 ? (
        <Card>
          <CardHeader>
            <CardTitle>Trace {traceId}</CardTitle>
            <div className="text-xs text-muted">
              {jobs.length} spans · Started {formatRelative(jobs[0].created_at)}
            </div>
          </CardHeader>
          <div className="space-y-1 mt-4">
            {jobs.map((job) => {
              const start = new Date(job.created_at || 0).getTime();
              const offset = Math.max(0, start - startTime);
              // A simple visual offset for hierarchy, though a real tree would be better
              // For now, simple waterfall by time
              return (
                <div key={job.job_id} className="relative group">
                  <div 
                    className="flex items-center gap-3 p-2 rounded-lg hover:bg-white/50 border border-transparent hover:border-border transition-colors"
                    style={{ marginLeft: `${Math.min(offset / 1000, 20)}px` }} // visual indentation by delay (fake tree)
                  >
                    <div className="w-1 h-full absolute left-0 bg-border" />
                    <div className="min-w-[140px] text-xs font-mono text-muted">
                       +{offset}ms
                    </div>
                    <JobStatusBadge state={job.state} />
                    <div className="flex-1">
                      <div className="text-sm font-semibold text-ink flex items-center gap-2">
                        {job.topic}
                        <span className="text-[10px] font-normal text-muted font-mono bg-white/50 px-1 rounded">
                          {job.job_id.slice(0, 8)}
                        </span>
                      </div>
                      {job.error_message ? (
                        <div className="text-xs text-danger mt-1">{job.error_message}</div>
                      ) : null}
                    </div>
                  </div>
                  {/* Connector line could go here */}
                </div>
              );
            })}
          </div>
        </Card>
      ) : query.data && jobs.length === 0 ? (
        <div className="p-4 text-sm text-muted">Trace found but contains no jobs (or access denied).</div>
      ) : null}
    </div>
  );
}
