/*
 * DESIGN: "Control Surface" — Jobs
 * Revision v2: Safety Decision column, Safety Decision filter, Pool filter
 * "Every job row tells the full story: who, what, governance decided, execution result, duration."
 */
import { useState, useMemo, useCallback } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
import { get } from "@/api/client";
import { mapJobRecord, type BackendJobRecord } from "@/api/transform";
import type { Job, SafetyDecisionType } from "@/api/types";
import { PageHeader } from "@/components/layout/PageHeader";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { Select } from "@/components/ui/Select";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonTable } from "@/components/ui/Skeleton";
import {
  Search, RefreshCw, ListChecks, Plus, Eye, Download,
  ArrowUpDown, ArrowUp, ArrowDown, Shield, X,
} from "lucide-react";
import { cn, formatRelativeTime, clickableRowProps } from "@/lib/utils";
import { toast } from "sonner";
import { useSubmitJob } from "@/hooks/useJobs";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";

function jobStatusVariant(status: string) {
  switch (status) {
    case "running": return "healthy" as const;
    case "succeeded": return "healthy" as const;
    case "failed": case "failed_fatal": return "danger" as const;
    case "failed_retryable": return "warning" as const;
    case "pending": case "scheduled": return "warning" as const;
    case "dispatched": return "info" as const;
    default: return "muted" as const;
  }
}

type SortKey = "status" | "id" | "topic" | "safety" | "attempts" | "updatedAt";
type SortDir = "asc" | "desc";

const statusOrder: Record<string, number> = {
  running: 0, pending: 1, scheduled: 2, dispatched: 3, succeeded: 4, failed: 5, failed_retryable: 5, failed_fatal: 6, cancelled: 7,
};

const safetyOrder: Record<string, number> = {
  deny: 0, require_approval: 1, throttle: 2, allow_with_constraints: 3, allow: 4,
};


function SubmitJobDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const navigate = useNavigate();
  const submitJob = useSubmitJob();
  const [topic, setTopic] = useState("");
  const [prompt, setPrompt] = useState("");
  const [priority, setPriority] = useState("normal");

  const handleSubmit = () => {
    if (!topic.trim() || !prompt.trim()) return;
    submitJob.mutate(
      { topic: topic.trim(), prompt: prompt.trim(), priority: priority as "low" | "normal" | "high" | "critical" },
      {
        onSuccess: (data) => {
          toast.success("Job submitted");
          onClose();
          setTopic("");
          setPrompt("");
          setPriority("normal");
          if (data.job_id) navigate(`/jobs/${data.job_id}`);
        },
        onError: (err) => toast.error(`Submission failed: ${err.message}`),
      },
    );
  };

  return (
    <AnimatePresence>
      {open && (
        <>
          <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} className="fixed inset-0 z-[90] bg-black/50 backdrop-blur-sm" onClick={onClose} />
          <motion.div
            initial={{ opacity: 0, scale: 0.95 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0, scale: 0.95 }}
            className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-[91] w-[520px] max-w-[90vw] bg-surface-1 border border-border rounded-xl shadow-2xl"
          >
            <div className="px-6 py-4 border-b border-border flex items-center justify-between">
              <h3 className="font-display font-semibold text-foreground">Submit Job</h3>
              <button onClick={onClose} className="p-1 rounded hover:bg-surface-2 text-muted-foreground"><X className="w-4 h-4" /></button>
            </div>
            <div className="px-6 py-5 space-y-4">
              <div>
                <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1">Topic *</label>
                <Input value={topic} onChange={(e) => setTopic(e.target.value)} placeholder="e.g. job.code-review" />
              </div>
              <div>
                <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1">Prompt *</label>
                <textarea
                  value={prompt}
                  onChange={(e) => setPrompt(e.target.value)}
                  rows={4}
                  placeholder="Describe the task for the agent..."
                  className="w-full px-3 py-2 text-xs bg-surface-0 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground/50 focus:outline-none focus:ring-1 focus:ring-cordum/30 resize-none"
                />
              </div>
              <div>
                <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1">Priority</label>
                <Select
                  value={priority}
                  onChange={(e) => setPriority(e.target.value)}
                  options={[
                    { value: "low", label: "Low" },
                    { value: "normal", label: "Normal" },
                    { value: "high", label: "High" },
                    { value: "critical", label: "Critical" },
                  ]}
                  className="w-40"
                />
              </div>
            </div>
            <div className="px-6 py-4 border-t border-border flex justify-end gap-2">
              <Button variant="outline" size="sm" onClick={onClose}>Cancel</Button>
              <Button
                variant="primary"
                size="sm"
                loading={submitJob.isPending}
                disabled={!topic.trim() || !prompt.trim()}
                onClick={handleSubmit}
              >
                Submit
              </Button>
            </div>
          </motion.div>
        </>
      )}
    </AnimatePresence>
  );
}

export default function JobsPage() {
  const navigate = useNavigate();
  const [search, setSearch] = useState("");
  const [activeTab, setActiveTab] = useState("all");
  const [safetyFilter, setSafetyFilter] = useState("all");
  const [showSubmit, setShowSubmit] = useState(false);
  const [sortKey, setSortKey] = useState<SortKey>("updatedAt");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  const { data, isLoading, refetch, dataUpdatedAt } = useQuery({
    queryKey: ["jobs"],
    queryFn: async () => {
      const res = await get<{ items: BackendJobRecord[]; total?: number }>("/jobs?limit=500");
      const items = (res.items ?? []).map(mapJobRecord).filter((j): j is Job => !!j);
      return { items, total: res.total ?? items.length };
    },
    refetchInterval: 10_000,
  });

  const jobs = data?.items ?? [];

  const enrichedJobs = useMemo(() => {
    return jobs.map((j) => ({
      ...j,
      _safetyDecision: j.safetyDecision?.type as string | undefined,
      _matchedRules: j.safetyDecision?.matchedRule ? [j.safetyDecision.matchedRule] : [],
    }));
  }, [jobs]);

  const tabs = useMemo(() => [
    { id: "all", label: "All", count: enrichedJobs.length },
    { id: "running", label: "Running", count: enrichedJobs.filter(j => j.status === "running").length },
    { id: "pending", label: "Pending", count: enrichedJobs.filter(j => j.status === "pending" || j.status === "scheduled").length },
    { id: "succeeded", label: "Completed", count: enrichedJobs.filter(j => j.status === "succeeded").length },
    { id: "failed", label: "Failed", count: enrichedJobs.filter(j => j.status === "failed").length },
  ], [enrichedJobs]);

  const safetyTabs = useMemo(() => [
    { id: "all", label: "All Decisions" },
    { id: "allow", label: "Allow" },
    { id: "deny", label: "Deny" },
    { id: "require_approval", label: "Approval" },
    { id: "allow_with_constraints", label: "Constrained" },
    { id: "throttle", label: "Throttle" },
  ], []);

  const toggleSort = useCallback((key: SortKey) => {
    if (sortKey === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setSortDir("desc");
    }
  }, [sortKey]);

  const filtered = useMemo(() => {
    let result = enrichedJobs.filter((j) => {
      // Status filter
      if (activeTab !== "all") {
        if (activeTab === "pending") {
          if (j.status !== "pending" && j.status !== "scheduled") return false;
        } else if (j.status !== activeTab) return false;
      }
      // Safety decision filter
      if (safetyFilter !== "all" && j._safetyDecision !== safetyFilter) return false;
      // Search
      if (search) {
        const q = search.toLowerCase();
        return (
          j.id.toLowerCase().includes(q) ||
          (j.topic ?? "").toLowerCase().includes(q) ||
          (j.traceId ?? "").toLowerCase().includes(q)
        );
      }
      return true;
    });

    result.sort((a, b) => {
      let cmp = 0;
      switch (sortKey) {
        case "status":
          cmp = (statusOrder[a.status] ?? 99) - (statusOrder[b.status] ?? 99);
          break;
        case "id":
          cmp = a.id.localeCompare(b.id);
          break;
        case "topic":
          cmp = (a.topic ?? "").localeCompare(b.topic ?? "");
          break;
        case "safety":
          cmp = (safetyOrder[a._safetyDecision as string] ?? 99) - (safetyOrder[b._safetyDecision as string] ?? 99);
          break;
        case "attempts":
          cmp = (a.attempts ?? 0) - (b.attempts ?? 0);
          break;
        case "updatedAt":
          cmp = new Date(a.updatedAt ?? 0).getTime() - new Date(b.updatedAt ?? 0).getTime();
          break;
      }
      return sortDir === "asc" ? cmp : -cmp;
    });

    return result;
  }, [enrichedJobs, activeTab, safetyFilter, search, sortKey, sortDir]);

  const exportCSV = () => {
    const rows = filtered.map((j) =>
      [j.id, j.status, j.topic ?? "", j._safetyDecision ?? "", j._matchedRules.join(";"), j.attempts ?? 0, j.updatedAt ?? ""].join(",")
    );
    const csv = ["id,status,topic,safety_decision,matched_rules,attempts,updatedAt", ...rows].join("\n");
    const blob = new Blob([csv], { type: "text/csv" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `jobs-export-${new Date().toISOString().slice(0, 10)}.csv`;
    a.click();
    URL.revokeObjectURL(url);
    toast.success(`Exported ${filtered.length} jobs`);
  };

  const SortIcon = ({ col }: { col: SortKey }) => {
    if (sortKey !== col) return <ArrowUpDown className="w-3 h-3 ml-1 opacity-30" />;
    return sortDir === "asc" ? <ArrowUp className="w-3 h-3 ml-1 text-cordum" /> : <ArrowDown className="w-3 h-3 ml-1 text-cordum" />;
  };

  const lastUpdated = dataUpdatedAt ? new Date(dataUpdatedAt) : null;

  return (
    <div className="space-y-6">
      <PageHeader
        label="Operate"
        title="Jobs"
        subtitle={`${data?.total ?? 0} total jobs across all states`}
        actions={
          <div className="flex items-center gap-2">
            {lastUpdated && (
              <span className="text-[10px] font-mono text-muted-foreground hidden md:inline">
                Updated {formatRelativeTime(lastUpdated.toISOString())}
              </span>
            )}
            <Button variant="outline" size="sm" onClick={exportCSV}>
              <Download className="w-3 h-3 mr-1" />
              CSV
            </Button>
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              <RefreshCw className="w-3 h-3 mr-1" />
              Refresh
            </Button>
            <Button variant="primary" size="sm" onClick={() => setShowSubmit(true)}>
              <Plus className="w-3 h-3 mr-1" />
              Submit Job
            </Button>
          </div>
        }
      />

      {/* Status Filters */}
      <div className="flex items-center gap-3 flex-wrap">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
          <input
            type="text"
            placeholder="Search by ID, topic, or trace..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="h-8 w-full pl-8 pr-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
          />
        </div>
        <div className="flex items-center gap-1 bg-surface-1 border border-border rounded-2xl p-0.5">
          {tabs.map((tab) => (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id)}
              className={cn(
                "px-3 py-1.5 text-xs font-medium rounded transition-colors",
                activeTab === tab.id
                  ? "bg-cordum/10 text-cordum"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {tab.label}
              {tab.count > 0 && (
                <span className="ml-1.5 px-1.5 py-0.5 rounded-full text-[10px] font-mono bg-surface-2">{tab.count}</span>
              )}
            </button>
          ))}
        </div>
      </div>

      {/* Safety Decision Filter */}
      <div className="flex items-center gap-2">
        <Shield className="w-3.5 h-3.5 text-muted-foreground" />
        <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Safety:</span>
        <div className="flex items-center gap-1">
          {safetyTabs.map((tab) => (
            <button
              key={tab.id}
              onClick={() => setSafetyFilter(tab.id)}
              className={cn(
                "px-2.5 py-1 text-[11px] font-medium rounded transition-colors",
                safetyFilter === tab.id
                  ? "bg-surface-2 text-foreground border border-border"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {tab.label}
            </button>
          ))}
        </div>
      </div>

      {/* Jobs Table */}
      {isLoading ? (
        <div className="instrument-card">
          <SkeletonTable rows={8} />
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<ListChecks className="w-5 h-5" />}
          title="No jobs found"
          description={search ? "Try adjusting your search or filters" : "No jobs have been submitted yet"}
          action={
            <Button variant="primary" size="sm" onClick={() => setShowSubmit(true)}>
              <Plus className="w-3 h-3 mr-1" />
              Submit Job
            </Button>
          }
        />
      ) : (
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3 }}
          className="instrument-card overflow-hidden"
        >
          <div className="overflow-x-auto">
          <table className="w-full min-w-[800px]">
            <thead>
              <tr className="border-b border-border bg-surface-0">
                <th
                  className="text-left px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                  onClick={() => toggleSort("status")}
                >
                  <span className="inline-flex items-center">Status <SortIcon col="status" /></span>
                </th>
                <th
                  className="text-left px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                  onClick={() => toggleSort("id")}
                >
                  <span className="inline-flex items-center">Job ID <SortIcon col="id" /></span>
                </th>
                <th
                  className="text-left px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                  onClick={() => toggleSort("topic")}
                >
                  <span className="inline-flex items-center">Topic <SortIcon col="topic" /></span>
                </th>
                <th
                  className="text-left px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                  onClick={() => toggleSort("safety")}
                >
                  <span className="inline-flex items-center">Safety Decision <SortIcon col="safety" /></span>
                </th>
                <th
                  className="text-center px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                  onClick={() => toggleSort("attempts")}
                >
                  <span className="inline-flex items-center justify-center">Attempts <SortIcon col="attempts" /></span>
                </th>
                <th
                  className="text-right px-5 py-2.5 text-[10px] font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                  onClick={() => toggleSort("updatedAt")}
                >
                  <span className="inline-flex items-center justify-end">Updated <SortIcon col="updatedAt" /></span>
                </th>
                <th className="px-5 py-2.5"></th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((job) => (
                <tr
                  key={job.id}
                  {...clickableRowProps(() => navigate(`/jobs/${job.id}`))}
                  className="border-b border-border hover:bg-surface-1 transition-colors cursor-pointer group"
                >
                  <td className="px-5 py-2.5">
                    <StatusBadge variant={jobStatusVariant(job.status)} dot pulse={job.status === "running"}>
                      {job.status}
                    </StatusBadge>
                  </td>
                  <td className="px-5 py-2.5 font-mono text-sm text-cordum group-hover:underline">{job.id.slice(0, 16)}</td>
                  <td className="px-5 py-2.5 text-sm text-foreground">{job.topic || "—"}</td>
                  <td className="px-5 py-2.5">
                    <SafetyDecisionBadge decision={job._safetyDecision} matchedRules={job._matchedRules} />
                  </td>
                  <td className="px-5 py-2.5 text-center font-mono text-xs text-muted-foreground">{job.attempts ?? 0}</td>
                  <td className="px-5 py-2.5 text-right text-xs text-muted-foreground font-mono">
                    {job.updatedAt ? formatRelativeTime(new Date(job.updatedAt).toISOString()) : "—"}
                  </td>
                  <td className="px-5 py-2.5">
                    <button className="p-1 rounded hover:bg-surface-2 transition-colors" aria-label="View details">
                      <Eye className="w-3.5 h-3.5 text-muted-foreground" />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          </div>
          <div className="flex items-center justify-between px-5 py-2.5 border-t border-border bg-surface-0">
            <span className="text-xs font-mono text-muted-foreground">
              Showing {filtered.length} of {enrichedJobs.length} jobs
            </span>
            <span className="text-[10px] font-mono text-muted-foreground">
              Sorted by {sortKey} ({sortDir})
            </span>
          </div>
        </motion.div>
      )}

      <SubmitJobDialog open={showSubmit} onClose={() => setShowSubmit(false)} />
    </div>
  );
}
