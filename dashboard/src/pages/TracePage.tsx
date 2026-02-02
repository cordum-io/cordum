import { useEffect, useMemo, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { epochToMillis, formatRelative } from "../lib/format";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Input } from "../components/ui/Input";
import { Button } from "../components/ui/Button";
import { JobStatusBadge } from "../components/StatusBadge";
import type { JobRecord } from "../types/api";

export function TracePage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const searchId = searchParams.get("id") || "";
  const [inputId, setInputId] = useState(id || searchId);
  
  const traceId = id || searchId;

  useEffect(() => {
    setInputId(id || searchId);
  }, [id, searchId]);

  const query = useQuery({
    queryKey: ["trace", traceId],
    queryFn: () => api.getTrace(traceId as string),
    enabled: Boolean(traceId),
  });

  const jobs = useMemo(() => {
    const raw = (query.data || []) as JobRecord[];
    return raw.sort((a, b) => {
      const tA = epochToMillis(a.updated_at) || 0;
      const tB = epochToMillis(b.updated_at) || 0;
      return tA - tB;
    });
  }, [query.data]);

  const startTime = jobs.length > 0 ? epochToMillis(jobs[0].updated_at) || 0 : 0;

  const handleSearch = () => {
    const value = inputId.trim();
    if (!value) {
      navigate("/trace");
      return;
    }
    navigate(`/trace/${value}`);
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
              {jobs.length} spans · Started {formatRelative(epochToMillis(jobs[0].updated_at) || 0)}
            </div>
          </CardHeader>
          <div className="space-y-1 mt-4">
            {jobs.map((job, index) => {
              const start = epochToMillis(job.updated_at) || 0;
              const offset = Math.max(0, start - startTime);
              // A simple visual offset for hierarchy, though a real tree would be better
              // For now, simple waterfall by time
              return (
                <div key={job.id || index} className="relative group">
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
                          {job.id?.slice(0, 8) || "unknown"}
                        </span>
                      </div>
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
