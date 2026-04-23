/*
 * DESIGN: "Control Surface" — Trace Browser
 * OBSERVE / Traces
 * Distributed execution visualization: waterfall, spans, cross-service timing
 */
import { useState, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
import { PageHeader } from "@/components/layout/PageHeader";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { SkeletonCard } from "@/components/ui/Skeleton";
import {
  Search, Activity, Clock, ChevronDown, ChevronRight,
  AlertTriangle, Shield, Cpu, Zap, ArrowRight, RefreshCw,
  Filter, ExternalLink, Loader2,
} from "lucide-react";
import { cn, formatRelativeTime } from "@/lib/utils";
import { toast } from "sonner";
import { get } from "@/api/client";
import { useJobs } from "@/hooks/useJobs";
import type { Trace, TraceSpan } from "@/api/types";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";

interface TraceEntry {
  trace_id: string;
  job_id: string;
  agent_id?: string;
  total_duration_ms: number;
  service_count?: number;
  span_count?: number;
  status: "ok" | "error" | "timeout";
  started_at: string;
  safety_decision?: string;
  topic?: string;
}

const serviceColors: Record<string, string> = {
  "API Gateway": "bg-blue-400",
  "Safety Kernel": "bg-cordum",
  "Scheduler": "bg-purple-400",
  "Message Bus": "bg-orange-400",
  "Worker Pool": "bg-cyan-400",
  "Worker": "bg-emerald-400",
};

function jobStatusToTraceStatus(status: string): "ok" | "error" | "timeout" {
  if (status === "failed") return "error";
  if (status === "timed_out") return "timeout";
  return "ok";
}

export default function TracesPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [selectedTrace, setSelectedTrace] = useState<string | null>(null);
  const [selectedSpan, setSelectedSpan] = useState<string | null>(null);

  // Derive traces from recent jobs with traceId
  const { data: jobsData, isLoading: jobsLoading } = useJobs({ limit: 50 });
  const jobs = jobsData?.items ?? [];

  const traces = useMemo<TraceEntry[]>(() => {
    const seen = new Map<string, TraceEntry>();
    for (const job of jobs) {
      if (!job.traceId) continue;
      if (seen.has(job.traceId)) continue;
      seen.set(job.traceId, {
        trace_id: job.traceId,
        job_id: job.id,
        total_duration_ms: job.duration ?? 0,
        status: jobStatusToTraceStatus(job.status),
        started_at: job.createdAt,
        safety_decision: job.safetyDecision?.type,
        topic: job.topic,
      });
    }
    return Array.from(seen.values());
  }, [jobs]);

  const filteredTraces = useMemo(() => {
    if (!search) return traces;
    const q = search.toLowerCase();
    return traces.filter(t =>
      t.trace_id.toLowerCase().includes(q) ||
      t.job_id.toLowerCase().includes(q) ||
      (t.topic ?? "").toLowerCase().includes(q)
    );
  }, [traces, search]);

  // Fetch trace detail when selected
  const { data: traceDetail, isLoading: traceLoading, error: traceError } = useQuery<Trace>({
    queryKey: ["trace", selectedTrace],
    queryFn: () => get<Trace>(`/traces/${selectedTrace}`),
    enabled: !!selectedTrace,
    retry: false,
    staleTime: 30_000,
  });

  const activeTraceEntry = selectedTrace ? traces.find(t => t.trace_id === selectedTrace) : null;

  // Compute spans with start_ms offsets from trace detail
  const spans = useMemo(() => {
    if (!traceDetail?.spans) return [];
    const traceStart = new Date(traceDetail.start_time).getTime();
    return traceDetail.spans.map((span) => {
      const spanStart = new Date(span.start_time).getTime();
      const startMs = isNaN(spanStart) || isNaN(traceStart) ? 0 : spanStart - traceStart;
      let durationMs = span.duration_ms ?? 0;
      if (!durationMs && span.end_time) {
        const endMs = new Date(span.end_time).getTime();
        if (!isNaN(endMs)) durationMs = endMs - spanStart;
      }
      return { ...span, start_ms: Math.max(0, startMs), duration_ms: Math.max(0, durationMs) };
    });
  }, [traceDetail]);

  const maxDuration = traceDetail?.total_duration_ms ?? (spans.length > 0 ? Math.max(...spans.map(s => s.start_ms + s.duration_ms)) : 1);

  // Collect unique services from spans for legend
  const spanServices = useMemo(() => {
    const set = new Set<string>();
    for (const s of spans) set.add(s.service);
    return Array.from(set);
  }, [spans]);

  return (
    <div className="space-y-6">
      <PageHeader
        label="Observe"
        title="Trace Browser"
        subtitle="Distributed execution visualization across services"
        actions={
          <Button variant="outline" size="sm" onClick={() => queryClient.invalidateQueries({ queryKey: ["jobs"] })}>
            <RefreshCw className="w-3 h-3 mr-1" />
            Refresh
          </Button>
        }
      />

      {/* Search */}
      <div className="relative max-w-lg">
        <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
        <input
          type="text"
          placeholder="Search by trace ID, job ID, or topic..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="h-8 w-full pl-8 pr-3 text-xs bg-surface-1 border border-border rounded-md text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
        />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        {/* Trace List */}
        <div className="space-y-2">
          <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest px-1">Recent Traces</p>
          {jobsLoading ? (
            <div className="space-y-2">
              <SkeletonCard /><SkeletonCard /><SkeletonCard />
            </div>
          ) : filteredTraces.length === 0 ? (
            <div className="instrument-card p-8 text-center">
              <Activity className="w-6 h-6 text-muted-foreground mx-auto mb-2" />
              <p className="text-xs text-muted-foreground">{search ? "No traces match your search" : "No traces found"}</p>
            </div>
          ) : (
            filteredTraces.map((trace) => (
              <div
                key={trace.trace_id}
                onClick={() => { setSelectedTrace(trace.trace_id); setSelectedSpan(null); }}
                className={cn(
                  "instrument-card p-3 cursor-pointer transition-all",
                  selectedTrace === trace.trace_id ? "ring-1 ring-cordum border-cordum/30" : "hover:bg-surface-1"
                )}
              >
                <div className="flex items-center justify-between mb-1.5">
                  <span className="font-mono text-xs text-cordum">{trace.trace_id.slice(0, 16)}</span>
                  <StatusBadge variant={trace.status === "ok" ? "healthy" : trace.status === "error" ? "danger" : "warning"}>
                    {trace.status}
                  </StatusBadge>
                </div>
                <div className="flex items-center gap-2 mb-1">
                  <span className="text-xs text-foreground">{trace.topic || "\u2014"}</span>
                  <SafetyDecisionBadge decision={trace.safety_decision} />
                </div>
                <div className="flex items-center gap-3 text-[10px] font-mono text-muted-foreground">
                  <span>{trace.total_duration_ms}ms</span>
                  <span>{formatRelativeTime(trace.started_at)}</span>
                </div>
              </div>
            ))
          )}
        </div>

        {/* Waterfall View */}
        <div className="lg:col-span-2">
          {activeTraceEntry ? (
            <div className="instrument-card overflow-hidden">
              <div className="px-5 py-3 border-b border-border flex items-center justify-between">
                <div>
                  <h3 className="font-display font-semibold text-sm text-foreground">Waterfall</h3>
                  <p className="text-xs text-muted-foreground font-mono">{activeTraceEntry.trace_id} · {activeTraceEntry.total_duration_ms}ms total</p>
                </div>
                <div className="flex items-center gap-2">
                  <Button variant="ghost" size="sm" onClick={() => navigate(`/jobs/${activeTraceEntry.job_id}`)}>
                    <ExternalLink className="w-3 h-3 mr-1" />
                    Job Detail
                  </Button>
                </div>
              </div>

              {traceLoading ? (
                <div className="p-8 flex items-center justify-center gap-2 text-xs text-muted-foreground">
                  <Loader2 className="w-4 h-4 animate-spin text-cordum" />
                  Loading trace spans...
                </div>
              ) : traceError ? (
                <div className="p-8 text-center">
                  <AlertTriangle className="w-6 h-6 text-muted-foreground mx-auto mb-2" />
                  <p className="text-sm text-foreground font-medium mb-1">Failed to load trace</p>
                  <p className="text-xs text-muted-foreground">Could not fetch trace data from the server</p>
                </div>
              ) : spans.length === 0 ? (
                <div className="p-8 text-center">
                  <Activity className="w-6 h-6 text-muted-foreground mx-auto mb-2" />
                  <p className="text-xs text-muted-foreground">No spans recorded for this trace</p>
                </div>
              ) : (
                <>
                  {/* Service legend */}
                  <div className="px-5 py-2 border-b border-border flex items-center gap-4 flex-wrap">
                    {spanServices.map((service) => (
                      <span key={service} className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
                        <span className={cn("w-2 h-2 rounded-sm", serviceColors[service] ?? "bg-gray-400")} />
                        {service}
                      </span>
                    ))}
                  </div>

                  {/* Timeline header */}
                  <div className="px-5 py-1.5 border-b border-border flex items-center">
                    <div className="w-40 shrink-0 text-[10px] font-mono text-muted-foreground">Operation</div>
                    <div className="flex-1 relative h-4">
                      {[0, 0.25, 0.5, 0.75, 1].map((pct) => (
                        <span
                          key={pct}
                          className="absolute top-0 text-[9px] font-mono text-muted-foreground/50 -translate-x-1/2"
                          style={{ left: `${pct * 100}%` }}
                        >
                          {Math.round(maxDuration * pct)}ms
                        </span>
                      ))}
                    </div>
                  </div>

                  {/* Spans */}
                  <div className="divide-y divide-border/50">
                    {spans.map((span) => {
                      const leftPct = maxDuration > 0 ? (span.start_ms / maxDuration) * 100 : 0;
                      const widthPct = maxDuration > 0 ? Math.max((span.duration_ms / maxDuration) * 100, 0.5) : 0.5;
                      const isSelected = selectedSpan === span.span_id;
                      const barColor = serviceColors[span.service] ?? "bg-gray-400";

                      return (
                        <div key={span.span_id}>
                          <div
                            className={cn(
                              "flex items-center px-5 py-2 cursor-pointer transition-colors",
                              isSelected ? "bg-surface-1" : "hover:bg-surface-0"
                            )}
                            onClick={() => setSelectedSpan(isSelected ? null : span.span_id)}
                          >
                            <div className="w-40 shrink-0 flex items-center gap-2">
                              {span.parent_span_id && <span className="w-3 border-l border-b border-border h-3 ml-1" />}
                              <div className="min-w-0">
                                <p className="text-xs text-foreground truncate">{span.operation}</p>
                                <p className="text-[10px] text-muted-foreground">{span.service}</p>
                              </div>
                            </div>
                            <div className="flex-1 relative h-6 flex items-center">
                              <div
                                className={cn("h-4 rounded-sm relative", barColor, span.status === "error" && "opacity-70")}
                                style={{ marginLeft: `${leftPct}%`, width: `${widthPct}%`, minWidth: "4px" }}
                              >
                                {span.safety_decision && (
                                  <div className="absolute -top-1 -right-1">
                                    <Shield className="w-3 h-3 text-cordum" />
                                  </div>
                                )}
                              </div>
                              <span className="ml-2 text-[10px] font-mono text-muted-foreground">{span.duration_ms}ms</span>
                            </div>
                          </div>

                          {/* Span detail */}
                          <AnimatePresence>
                            {isSelected && (
                              <motion.div
                                initial={{ height: 0, opacity: 0 }}
                                animate={{ height: "auto", opacity: 1 }}
                                exit={{ height: 0, opacity: 0 }}
                                className="bg-surface-0 border-t border-border"
                              >
                                <div className="px-5 py-3 grid grid-cols-2 lg:grid-cols-4 gap-3 text-xs">
                                  <div>
                                    <p className="text-muted-foreground mb-0.5">Span ID</p>
                                    <p className="font-mono text-foreground">{span.span_id}</p>
                                  </div>
                                  <div>
                                    <p className="text-muted-foreground mb-0.5">Service</p>
                                    <p className="text-foreground">{span.service}</p>
                                  </div>
                                  <div>
                                    <p className="text-muted-foreground mb-0.5">Duration</p>
                                    <p className="font-mono text-foreground">{span.duration_ms}ms</p>
                                  </div>
                                  <div>
                                    <p className="text-muted-foreground mb-0.5">Status</p>
                                    <StatusBadge variant={span.status === "ok" ? "healthy" : "danger"}>{span.status}</StatusBadge>
                                  </div>
                                  {span.safety_decision && (
                                    <div>
                                      <p className="text-muted-foreground mb-0.5">Safety Decision</p>
                                      <SafetyDecisionBadge decision={span.safety_decision} />
                                    </div>
                                  )}
                                  {span.attributes && Object.entries(span.attributes).map(([k, v]) => (
                                    <div key={k}>
                                      <p className="text-muted-foreground mb-0.5">{k}</p>
                                      <p className="font-mono text-foreground">{String(v)}</p>
                                    </div>
                                  ))}
                                  {span.error_message && (
                                    <div className="col-span-full">
                                      <p className="text-muted-foreground mb-0.5">Error</p>
                                      <p className="font-mono text-red-400 text-xs">{span.error_message}</p>
                                    </div>
                                  )}
                                </div>
                              </motion.div>
                            )}
                          </AnimatePresence>
                        </div>
                      );
                    })}
                  </div>
                </>
              )}
            </div>
          ) : (
            <div className="instrument-card p-12 text-center">
              <Activity className="w-8 h-8 text-muted-foreground mx-auto mb-3" />
              <p className="text-sm text-foreground font-medium">Select a trace</p>
              <p className="text-xs text-muted-foreground mt-1">Click a trace from the list to view its waterfall</p>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
