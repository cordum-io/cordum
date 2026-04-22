/*
 * DESIGN: "Control Surface" — Jobs
 * Revision v2: Safety Decision column, Safety Decision filter, Pool filter
 * "Every job row tells the full story: who, what, governance decided, execution result, duration."
 */
import { useState, useMemo, useCallback } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
import { get } from "@/api/client";
import { mapJobRecord, type BackendJobRecord } from "@/api/transform";
import type { Job, SafetyDecisionType } from "@/api/types";
import { PageHeader } from "@/components/layout/PageHeader";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { DialogOverlay } from "@/components/ui/DialogOverlay";
import { LabeledField } from "@/components/ui/LabeledField";
import { Input } from "@/components/ui/Input";
import { Select } from "@/components/ui/Select";
import { EmptyState } from "@/components/ui/EmptyState";
import { Pagination } from "@/components/ui/Pagination";
import { SkeletonTable } from "@/components/ui/Skeleton";
import { Tabs } from "@/components/ui/Tabs";
import { Textarea } from "@/components/ui/Textarea";
import {
  Search,
  RefreshCw,
  ListChecks,
  Plus,
  Eye,
  Download,
  ArrowUpDown,
  ArrowUp,
  ArrowDown,
  Shield,
  X,
} from "lucide-react";
import { cn, formatRelativeTime, clickableRowProps } from "@/lib/utils";
import { friendlyError } from "@/lib/friendlyError";
import { toast } from "sonner";
import { useSubmitJob } from "@/hooks/useJobs";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { safeLocalStorage } from "@/lib/storage";

function jobStatusVariant(status: string) {
  switch (status) {
    case "running":
      return "healthy" as const;
    case "succeeded":
      return "healthy" as const;
    case "failed":
    case "failed_fatal":
      return "danger" as const;
    case "denied":
      return "governance" as const;
    case "failed_retryable":
      return "warning" as const;
    case "pending":
    case "scheduled":
      return "warning" as const;
    case "dispatched":
      return "info" as const;
    default:
      return "muted" as const;
  }
}

type SortKey = "status" | "id" | "topic" | "safety" | "attempts" | "updatedAt";
type SortDir = "asc" | "desc";

const statusOrder: Record<string, number> = {
  running: 0,
  pending: 1,
  scheduled: 2,
  dispatched: 3,
  succeeded: 4,
  failed: 5,
  failed_retryable: 5,
  failed_fatal: 6,
  cancelled: 7,
};

const safetyOrder: Record<string, number> = {
  deny: 0,
  require_approval: 1,
  throttle: 2,
  allow_with_constraints: 3,
  allow: 4,
};

export function readStoredJobsPageSize(): number {
  const raw = safeLocalStorage.getItem("cordum-jobs-page-size");
  const parsed = Number.parseInt(raw ?? "", 10);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return 50;
  }
  return parsed;
}

export function SubmitJobDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const submitJob = useSubmitJob();
  const [topic, setTopic] = useState("");
  const [prompt, setPrompt] = useState("");
  const [priority, setPriority] = useState("normal");

  const handleSubmit = () => {
    if (!topic.trim() || !prompt.trim()) return;
    submitJob.mutate(
      {
        topic: topic.trim(),
        prompt: prompt.trim(),
        priority: priority as "low" | "normal" | "high" | "critical",
      },
      {
        onSuccess: (data) => {
          toast.success("Job submitted");
          onClose();
          setTopic("");
          setPrompt("");
          setPriority("normal");
          if (data.job_id) navigate(`/jobs/${data.job_id}`);
        },
        onError: (err) => {
          const friendly = friendlyError(err, "submit job");
          toast.error(friendly.title, { description: friendly.description });
        },
      },
    );
  };

  return (
    <DialogOverlay
      open={open}
      onClose={onClose}
      label="Submit Job"
      initialFocusSelector='input[aria-label="Job topic"]'
      className="w-[520px] max-w-[90vw] overflow-hidden rounded-3xl border border-border bg-surface-1 shadow-2xl"
    >
      <div className="border-b border-border px-6 py-4">
        <div className="flex items-center justify-between gap-4">
          <div>
            <h2
              id="submit-job-dialog-title"
              className="font-display font-semibold text-foreground"
            >
              Submit Job
            </h2>
            <p className="mt-1 text-xs text-muted-foreground">
              Dispatch a new job to the control plane with a topic, prompt, and priority.
            </p>
          </div>
          <Button
            variant="ghost"
            size="icon"
            onClick={onClose}
            aria-label="Close submit job dialog"
            className="h-8 w-8"
          >
            <X className="h-4 w-4" />
          </Button>
        </div>
      </div>
      <div className="space-y-4 px-6 py-5">
        <LabeledField
          label="Topic *"
          description="Choose the routing topic the workers listen on."
        >
          <Input
            value={topic}
            onChange={(e) => setTopic(e.target.value)}
            placeholder="e.g. job.code-review"
            aria-label="Job topic"
          />
        </LabeledField>
        <LabeledField
          label="Prompt *"
          description="Describe the work the agent should execute."
        >
          <Textarea
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            rows={4}
            placeholder="Describe the task for the agent..."
            aria-label="Job prompt"
            className="min-h-[7rem] resize-none"
          />
        </LabeledField>
        <LabeledField
          label="Priority"
          description="Escalate only when the job should jump ahead of normal work."
        >
          <Select
            value={priority}
            onChange={(e) => setPriority(e.target.value)}
            aria-label="Job priority"
            options={[
              { value: "low", label: "Low" },
              { value: "normal", label: "Normal" },
              { value: "high", label: "High" },
              { value: "critical", label: "Critical" },
            ]}
            className="w-full sm:w-48"
          />
        </LabeledField>
      </div>
      <div className="flex justify-end gap-2 border-t border-border px-6 py-4">
        <Button variant="outline" size="sm" onClick={onClose}>
          Cancel
        </Button>
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
    </DialogOverlay>
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
  const [searchParams, setSearchParams] = useSearchParams();
  const page = Math.max(1, parseInt(searchParams.get("page") ?? "1", 10));
  const [pageSize, setPageSize] = useState(readStoredJobsPageSize);

  const { data, isLoading, isError, error, refetch, dataUpdatedAt } = useQuery({
    queryKey: ["jobs"],
    queryFn: async () => {
      const res = await get<{ items: BackendJobRecord[]; total?: number }>(
        "/jobs?limit=500",
      );
      const items = (res.items ?? [])
        .map(mapJobRecord)
        .filter((j): j is Job => !!j);
      return { items, total: res.total ?? items.length };
    },
    refetchInterval: 10_000,
  });

  const jobs = data?.items ?? [];

  const enrichedJobs = useMemo(() => {
    return jobs.map((j) => ({
      ...j,
      _safetyDecision: j.safetyDecision?.type as string | undefined,
      _matchedRules: j.safetyDecision?.matchedRule
        ? [j.safetyDecision.matchedRule]
        : [],
    }));
  }, [jobs]);

  const tabs = useMemo(
    () => [
      { id: "all", label: "All", count: enrichedJobs.length },
      {
        id: "running",
        label: "Running",
        count: enrichedJobs.filter((j) => j.status === "running").length,
      },
      {
        id: "pending",
        label: "Pending",
        count: enrichedJobs.filter(
          (j) => j.status === "pending" || j.status === "scheduled",
        ).length,
      },
      {
        id: "succeeded",
        label: "Completed",
        count: enrichedJobs.filter((j) => j.status === "succeeded").length,
      },
      {
        id: "failed",
        label: "Failed",
        count: enrichedJobs.filter((j) => j.status === "failed").length,
      },
    ],
    [enrichedJobs],
  );

  const safetyTabs = useMemo(
    () => [
      { id: "all", label: "All Decisions" },
      { id: "allow", label: "Allow" },
      { id: "deny", label: "Deny" },
      { id: "require_approval", label: "Approval" },
      { id: "allow_with_constraints", label: "Constrained" },
      { id: "throttle", label: "Throttle" },
    ],
    [],
  );

  const toggleSort = useCallback(
    (key: SortKey) => {
      if (sortKey === key) {
        setSortDir((d) => (d === "asc" ? "desc" : "asc"));
      } else {
        setSortKey(key);
        setSortDir("desc");
      }
    },
    [sortKey],
  );

  /** Returns aria + keyboard props for a sortable column header. */
  const sortableThProps = useCallback(
    (col: SortKey) => ({
      role: "columnheader" as const,
      tabIndex: 0,
      "aria-sort": (sortKey === col
        ? sortDir === "asc"
          ? "ascending"
          : "descending"
        : "none") as "ascending" | "descending" | "none",
      onClick: () => toggleSort(col),
      onKeyDown: (e: React.KeyboardEvent) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          toggleSort(col);
        }
      },
    }),
    [sortKey, sortDir, toggleSort],
  );

  const filtered = useMemo(() => {
    let result = enrichedJobs.filter((j) => {
      // Status filter
      if (activeTab !== "all") {
        if (activeTab === "pending") {
          if (j.status !== "pending" && j.status !== "scheduled") return false;
        } else if (j.status !== activeTab) return false;
      }
      // Safety decision filter
      if (safetyFilter !== "all" && j._safetyDecision !== safetyFilter)
        return false;
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
          cmp =
            (safetyOrder[a._safetyDecision as string] ?? 99) -
            (safetyOrder[b._safetyDecision as string] ?? 99);
          break;
        case "attempts":
          cmp = (a.attempts ?? 0) - (b.attempts ?? 0);
          break;
        case "updatedAt":
          cmp =
            new Date(a.updatedAt ?? 0).getTime() -
            new Date(b.updatedAt ?? 0).getTime();
          break;
      }
      return sortDir === "asc" ? cmp : -cmp;
    });

    return result;
  }, [enrichedJobs, activeTab, safetyFilter, search, sortKey, sortDir]);

  // Pagination — clamp page if filtered set shrinks
  const totalPages = Math.max(1, Math.ceil(filtered.length / pageSize));
  const safePage = Math.min(page, totalPages);
  const startIdx = (safePage - 1) * pageSize;
  const paginatedJobs = useMemo(
    () => filtered.slice(startIdx, startIdx + pageSize),
    [filtered, startIdx, pageSize],
  );

  const handlePageChange = useCallback(
    (p: number) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (p <= 1) next.delete("page");
          else next.set("page", String(p));
          return next;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  const handlePageSizeChange = useCallback(
    (size: number) => {
      setPageSize(size);
      safeLocalStorage.setItem("cordum-jobs-page-size", String(size));
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          next.delete("page");
          return next;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  // Reset page when filters change
  const setActiveTabAndResetPage = useCallback(
    (tab: string) => {
      setActiveTab(tab);
      handlePageChange(1);
    },
    [handlePageChange],
  );

  const exportCSV = () => {
    const rows = filtered.map((j) =>
      [
        j.id,
        j.status,
        j.topic ?? "",
        j._safetyDecision ?? "",
        j._matchedRules.join(";"),
        j.attempts ?? 0,
        j.updatedAt ?? "",
      ].join(","),
    );
    const csv = [
      "id,status,topic,safety_decision,matched_rules,attempts,updatedAt",
      ...rows,
    ].join("\n");
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
    if (sortKey !== col)
      return <ArrowUpDown className="w-3 h-3 ml-1 opacity-30" />;
    return sortDir === "asc" ? (
      <ArrowUp className="w-3 h-3 ml-1 text-cordum" />
    ) : (
      <ArrowDown className="w-3 h-3 ml-1 text-cordum" />
    );
  };

  const lastUpdated = dataUpdatedAt ? new Date(dataUpdatedAt) : null;

  if (isError) {
    return (
      <ErrorBanner
        message={error instanceof Error ? error.message : "Failed to load jobs"}
        onRetry={() => void refetch()}
      />
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        label="Operate"
        title="Jobs"
        subtitle={`${data?.total ?? 0} total jobs across all states`}
        actions={
          <div className="flex items-center gap-2">
            {lastUpdated && (
              <span className="text-xs font-mono text-muted-foreground hidden md:inline">
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
            <Button
              variant="primary"
              size="sm"
              onClick={() => setShowSubmit(true)}
            >
              <Plus className="w-3 h-3 mr-1" />
              Submit Job
            </Button>
          </div>
        }
      />

      {/* Status Filters */}
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="text"
          placeholder="Search by ID, topic, or trace..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          icon={<Search className="h-3.5 w-3.5" />}
          className="h-9 max-w-sm flex-1 text-sm"
        />
        <Tabs
          tabs={tabs}
          activeTab={activeTab}
          onChange={setActiveTabAndResetPage}
          variant="segmented"
          ariaLabel="Job status filters"
          className="w-full sm:w-auto"
        />
      </div>

      {/* Safety Decision Filter */}
      <div className="space-y-2">
        <div className="flex items-center gap-2 text-xs font-mono uppercase tracking-widest text-muted-foreground">
          <Shield className="h-3.5 w-3.5" />
          <span>Safety decision</span>
        </div>
        <Tabs
          tabs={safetyTabs}
          activeTab={safetyFilter}
          onChange={setSafetyFilter}
          variant="segmented"
          ariaLabel="Safety decision filters"
          className="w-full"
        />
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
          description={
            search || activeTab !== "all" || safetyFilter !== "all"
              ? "Try adjusting your search or filters"
              : "No jobs have been submitted yet"
          }
          action={
            search || activeTab !== "all" || safetyFilter !== "all" ? (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setSearch("");
                  setActiveTabAndResetPage("all");
                  setSafetyFilter("all");
                }}
              >
                <X className="w-3 h-3 mr-1" />
                Clear all filters
              </Button>
            ) : (
              <Button
                variant="primary"
                size="sm"
                onClick={() => setShowSubmit(true)}
              >
                <Plus className="w-3 h-3 mr-1" />
                Submit Job
              </Button>
            )
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
                    className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                    {...sortableThProps("status")}
                  >
                    <span className="inline-flex items-center">
                      Status <SortIcon col="status" />
                    </span>
                  </th>
                  <th
                    className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                    {...sortableThProps("id")}
                  >
                    <span className="inline-flex items-center">
                      Job ID <SortIcon col="id" />
                    </span>
                  </th>
                  <th
                    className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                    {...sortableThProps("topic")}
                  >
                    <span className="inline-flex items-center">
                      Topic <SortIcon col="topic" />
                    </span>
                  </th>
                  <th
                    className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                    {...sortableThProps("safety")}
                  >
                    <span className="inline-flex items-center">
                      Safety Decision <SortIcon col="safety" />
                    </span>
                  </th>
                  <th
                    className="text-center px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                    {...sortableThProps("attempts")}
                  >
                    <span className="inline-flex items-center justify-center">
                      Attempts <SortIcon col="attempts" />
                    </span>
                  </th>
                  <th
                    className="text-right px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest cursor-pointer select-none hover:text-foreground transition-colors"
                    onClick={() => toggleSort("updatedAt")}
                  >
                    <span className="inline-flex items-center justify-end">
                      Updated <SortIcon col="updatedAt" />
                    </span>
                  </th>
                  <th className="px-5 py-3"></th>
                </tr>
              </thead>
              <tbody>
                {paginatedJobs.map((job) => (
                  <tr
                    key={job.id}
                    {...clickableRowProps(() => navigate(`/jobs/${job.id}`))}
                    className="border-b border-border hover:bg-surface-1 transition-colors cursor-pointer group"
                  >
                    <td className="px-5 py-3">
                      <StatusBadge
                        variant={jobStatusVariant(job.status)}
                        dot
                        pulse={job.status === "running"}
                      >
                        {job.status}
                      </StatusBadge>
                      {job.labels?.safety_bypassed === "true" && (
                        <StatusBadge
                          variant="warning"
                          className="ml-1.5"
                        >
                          Bypassed
                        </StatusBadge>
                      )}
                    </td>
                    <td className="px-5 py-3 font-mono text-sm text-cordum group-hover:underline">
                      {job.id.slice(0, 16)}
                    </td>
                    <td className="px-5 py-3 text-sm text-foreground">
                      {job.topic || "—"}
                    </td>
                    <td className="px-5 py-3">
                      <SafetyDecisionBadge
                        decision={job._safetyDecision}
                        matchedRules={job._matchedRules}
                      />
                    </td>
                    <td className="px-5 py-3 text-center font-mono text-xs text-muted-foreground">
                      {job.attempts ?? 0}
                    </td>
                    <td className="px-5 py-3 text-right text-xs text-muted-foreground font-mono">
                      {job.updatedAt
                        ? formatRelativeTime(
                            new Date(job.updatedAt).toISOString(),
                          )
                        : "—"}
                    </td>
                    <td className="px-5 py-3">
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7"
                        aria-label="View details"
                      >
                        <Eye className="w-3.5 h-3.5 text-muted-foreground" />
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="px-5 py-2 border-t border-border bg-surface-0">
            <Pagination
              page={safePage}
              pageSize={pageSize}
              total={filtered.length}
              onPageChange={handlePageChange}
              onPageSizeChange={handlePageSizeChange}
            />
          </div>
          <div className="flex items-center justify-between px-5 py-2 text-xs font-mono text-muted-foreground">
            <span>
              {filtered.length} of {enrichedJobs.length} jobs (sorted by{" "}
              {sortKey} {sortDir})
            </span>
          </div>
        </motion.div>
      )}

      <SubmitJobDialog open={showSubmit} onClose={() => setShowSubmit(false)} />
    </div>
  );
}
