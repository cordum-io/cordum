/*
 * DESIGN: "Control Surface" — Jobs
 * Phase 3 wk4 (task-2c3c8a04) rewrite: filter state migrated to nuqs (URL-driven,
 * shareable links); hand-rolled <table> swapped for primitives/DataTable
 * (sortable, virtualized at >100 rows).
 * Phase 3 wk4 follow-up (task-0bcb9411): DLQ folded in as a status filter —
 * `?status=dlq` swaps the data source to useDLQ, surfaces a "failed terminally"
 * banner, and adds an Actions column with Replay (idempotency-confirm) and Drop
 * (type-to-confirm) per the DLQ-specific UX. JobDetailPage tab decomposition is
 * still deferred to task-90bb5ef3.
 */
import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { useQueryState } from "nuqs";
import type { ColumnDef } from "@tanstack/react-table";
import { get } from "@/api/client";
import { mapJobRecord, type BackendJobRecord } from "@/api/transform";
import type { DLQEntry, Job } from "@/api/types";
import { PageHeader } from "@/components/layout/PageHeader";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Button } from "@/components/ui/Button";
import { DialogOverlay } from "@/components/ui/DialogOverlay";
import { LabeledField } from "@/components/ui/LabeledField";
import { Input } from "@/components/ui/Input";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { Select } from "@/components/ui/Select";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonTable } from "@/components/ui/Skeleton";
import { Tabs } from "@/components/ui/Tabs";
import { Textarea } from "@/components/ui/Textarea";
import { DataTable, type DecisionTier } from "@/components/primitives/DataTable";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { useDLQ, useRetryDLQ, useDeleteDLQ } from "@/hooks/useDLQ";
import {
  Search,
  RefreshCw,
  ListChecks,
  Plus,
  Download,
  RotateCcw,
  Shield,
  Trash2,
  X,
  Workflow,
  MessageSquare,
  Zap,
} from "lucide-react";
import { formatRelativeTime } from "@/lib/utils";
import { friendlyError } from "@/lib/friendlyError";
import { getJobParentRefs } from "@/lib/jobParentRefs";
import { toast } from "sonner";
import { useSubmitJob } from "@/hooks/useJobs";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import {
  parseAsEnum,
  parseAsSearchTerm,
} from "@/lib/url-state";
import { JobFiltersBar, type JobFilterValues } from "@/components/jobs/JobFiltersBar";

export function OriginPill({ job }: { job: Job }) {
  const navigate = useNavigate();
  const { runId, sessionId, workflowId } = getJobParentRefs(job);

  if (runId && workflowId) {
    return (
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          navigate(`/workflows/${workflowId}/runs/${runId}`);
        }}
        className="inline-flex items-center gap-1 rounded-full bg-primary/10 px-2 py-0.5 text-[10px] font-medium text-primary hover:bg-primary/20 transition"
      >
        <Workflow className="h-2.5 w-2.5" />
        Run: {runId.slice(0, 8)}
      </button>
    );
  }

  if (sessionId) {
    return (
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          navigate(`/copilot/sessions/${sessionId}`);
        }}
        className="inline-flex items-center gap-1 rounded-full bg-cordum/10 px-2 py-0.5 text-[10px] font-medium text-cordum hover:bg-cordum/20 transition"
      >
        <MessageSquare className="h-2.5 w-2.5" />
        Session: {sessionId.slice(0, 8)}
      </button>
    );
  }

  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-muted/30 px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
      <Zap className="h-2.5 w-2.5" />
      Direct
    </span>
  );
}

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

/** URL-state filter tabs. 'all' is the default; the rest match the Job
 *  status enum. 'dlq' is reserved for the Phase-3-wk4 follow-up that wires
 *  the DLQ data-source fold (see PR body). */
const STATUS_FILTERS = [
  "all",
  "running",
  "pending",
  "succeeded",
  "failed",
  "denied",
  "dlq",
] as const;

/** Safety-decision filter values. 'all' is the default; the rest mirror
 *  SafetyDecisionType. Synced with the segmented Tabs control. */
const SAFETY_FILTERS = [
  "all",
  "allow",
  "deny",
  "require_approval",
  "allow_with_constraints",
  "throttle",
] as const;

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

type EnrichedJob = Job & {
  _safetyDecision?: string;
  _matchedRules: string[];
  /** When true, this row is a DLQ entry rendered through the DLQ-mode column
   *  set (Actions menu visible). DLQ entries don't carry a safetyDecision so
   *  the SafetyDecisionBadge column renders "—" for them. */
  _dlq?: boolean;
};

/** Coerce a DLQEntry into the EnrichedJob row shape the DataTable consumes.
 *  Field map:
 *    id            ← entry.id (the DLQ entry id, NOT entry.jobId; the actions
 *                    operate on the DLQ entry id per useRetryDLQ/useDeleteDLQ
 *                    contract).
 *    topic         ← entry.originalTopic (the topic the failed job was on)
 *    status        ← "dlq" (literal — DLQ entries don't have a Job status)
 *    updatedAt     ← entry.failedAt
 *    attempts      ← entry.retryCount
 *    safetyDecision/labels/etc — undefined; the safety-decision badge column
 *                    renders "—" for missing values.
 *  Kept inline (not extracted to a util) because it's a single-consumer
 *  coercion specific to this page's DataTable shape — extracting would force
 *  callers to know about the EnrichedJob private type.
 */
function dlqEntryToEnrichedJob(entry: DLQEntry): EnrichedJob {
  const failedAt = entry.failedAt ?? new Date().toISOString();
  return {
    id: entry.id,
    type: entry.originalTopic ?? "",
    topic: entry.originalTopic ?? "",
    status: "denied", // closest Job status to "terminal failure"; row-render uses _dlq flag for the actual badge
    pool: "",
    capabilities: [],
    riskTags: [],
    metadata: {},
    createdAt: failedAt,
    updatedAt: failedAt,
    attempts: entry.retryCount,
    _safetyDecision: undefined,
    _matchedRules: [],
    _dlq: true,
  };
}

export default function JobsPage() {
  const navigate = useNavigate();
  const [search, setSearch] = useQueryState(
    "q",
    parseAsSearchTerm.withOptions({ clearOnDefault: true }),
  );
  const [activeTab, setActiveTab] = useQueryState(
    "status",
    parseAsEnum(STATUS_FILTERS, "all").withOptions({ clearOnDefault: true }),
  );
  const [safetyFilter, setSafetyFilter] = useQueryState(
    "safety",
    parseAsEnum(SAFETY_FILTERS, "all").withOptions({ clearOnDefault: true }),
  );
  const [jobFilters, setJobFilters] = useState<JobFilterValues>({});
  const [showSubmit, setShowSubmit] = useState(false);
  // DLQ row-action confirm-dialog state. Each pending action holds the entry
  // id so the dialog's Confirm handler knows which row to operate on.
  const [pendingReplay, setPendingReplay] = useState<string | null>(null);
  const [pendingDrop, setPendingDrop] = useState<string | null>(null);

  const isDlqMode = activeTab === "dlq";

  const jobsQuery = useQuery({
    queryKey: ["jobs", jobFilters],
    queryFn: async () => {
      const q = new URLSearchParams();
      // Bumped from 500 → 1000 to take advantage of primitives/DataTable
      // virtualization (DOM-node count stays bounded regardless of row count).
      q.set("limit", "1000");
      if (jobFilters.topic) q.set("topic", jobFilters.topic);
      if (jobFilters.pool) q.set("pool", jobFilters.pool);
      if (jobFilters.tenant) q.set("tenant", jobFilters.tenant);
      if (jobFilters.runId) q.set("run_id", jobFilters.runId);
      if (jobFilters.sessionId) q.set("session_id", jobFilters.sessionId);

      const res = await get<{ items: BackendJobRecord[]; total?: number }>(
        `/jobs?${q.toString()}`,
      );
      const items = (res.items ?? [])
        .map(mapJobRecord)
        .filter((j): j is Job => !!j);
      return { items, total: res.total ?? items.length };
    },
    refetchInterval: 10_000,
    enabled: !isDlqMode,
  });

  // DLQ mode swaps the data source. useDLQ hits `/api/v1/dlq/page` and returns
  // DLQEntry records — adapter coerces those into the same EnrichedJob row
  // shape so the DataTable column set works without conditional cell renderers
  // that branch on Job-vs-DLQEntry. Replay/Drop mutations live below.
  const dlqQuery = useDLQ({ limit: 1000 });
  const retryMutation = useRetryDLQ();
  const dropMutation = useDeleteDLQ();

  const data = isDlqMode
    ? dlqQuery.data?.items
      ? {
          items: dlqQuery.data.items.map((entry) =>
            dlqEntryToEnrichedJob(entry),
          ),
          total: dlqQuery.data.items.length,
        }
      : undefined
    : jobsQuery.data;
  const isLoading = isDlqMode ? dlqQuery.isLoading : jobsQuery.isLoading;
  const isError = isDlqMode ? dlqQuery.isError : jobsQuery.isError;
  const error = isDlqMode ? dlqQuery.error : jobsQuery.error;
  const refetch = isDlqMode ? dlqQuery.refetch : jobsQuery.refetch;
  const dataUpdatedAt = isDlqMode
    ? dlqQuery.dataUpdatedAt
    : jobsQuery.dataUpdatedAt;

  const jobs = data?.items ?? [];

  const enrichedJobs = useMemo<EnrichedJob[]>(() => {
    if (isDlqMode) {
      // DLQ path — already adapted, just pass through.
      return jobs as EnrichedJob[];
    }
    return jobs.map((j) => ({
      ...j,
      _safetyDecision: j.safetyDecision?.type as string | undefined,
      _matchedRules: j.safetyDecision?.matchedRule
        ? [j.safetyDecision.matchedRule]
        : [],
    }));
  }, [jobs, isDlqMode]);

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

  // Filter set for the table — sort is handled by DataTable's TanStack
  // getSortedRowModel, so we no longer maintain sortKey/sortDir/toggleSort
  // or the per-column sort comparators (those moved to ColumnDef.sortingFn
  // and the inferred default lexical/numeric sort).
  const filtered = useMemo<EnrichedJob[]>(() => {
    return enrichedJobs.filter((j) => {
      if (activeTab !== "all" && activeTab !== "dlq") {
        if (activeTab === "pending") {
          if (j.status !== "pending" && j.status !== "scheduled") return false;
        } else if (j.status !== activeTab) return false;
      }
      if (safetyFilter !== "all" && j._safetyDecision !== safetyFilter)
        return false;
      if (jobFilters.state && jobFilters.state.length > 0) {
        if (!jobFilters.state.includes(j.status)) return false;
      }
      if (jobFilters.decision && jobFilters.decision.length > 0) {
        if (!j._safetyDecision || !jobFilters.decision.includes(j._safetyDecision))
          return false;
      }
      if (search) {
        const q = search.toLowerCase();
        return (
          j.id.toLowerCase().includes(q) ||
          (j.topic ?? "").toLowerCase().includes(q) ||
          (j.traceId ?? "").toLowerCase().includes(q) ||
          (j.workflowRunId ?? "").toLowerCase().includes(q)
        );
      }
      return true;
    });
  }, [enrichedJobs, activeTab, safetyFilter, search, jobFilters]);

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

  const lastUpdated = dataUpdatedAt ? new Date(dataUpdatedAt) : null;

  const decisionAccessor = (job: EnrichedJob): DecisionTier | undefined =>
    job._safetyDecision as DecisionTier | undefined;

  const columns = useMemo<ColumnDef<EnrichedJob>[]>(
    () => [
      {
        id: "status",
        header: "Status",
        accessorFn: (j) => (j._dlq ? "dlq" : j.status),
        cell: ({ row }) =>
          row.original._dlq ? (
            <StatusBadge variant="danger" dot>
              dlq
            </StatusBadge>
          ) : (
            <div className="flex items-center gap-1.5">
              <StatusBadge
                variant={jobStatusVariant(row.original.status)}
                dot
                pulse={row.original.status === "running"}
              >
                {row.original.status}
              </StatusBadge>
              {row.original.labels?.safety_bypassed === "true" && (
                <StatusBadge variant="warning">Bypassed</StatusBadge>
              )}
            </div>
          ),
      },
      {
        id: "id",
        header: "Job ID",
        accessorFn: (j) => j.id,
        cell: ({ row }) => (
          <span className="font-mono text-cordum">
            {row.original.id.slice(0, 16)}
          </span>
        ),
      },
      {
        id: "topic",
        header: "Topic",
        accessorFn: (j) => j.topic ?? "",
        cell: ({ row }) => (
          <span className="text-foreground">{row.original.topic || "—"}</span>
        ),
      },
      {
        id: "origin",
        header: "Origin",
        enableSorting: false,
        cell: ({ row }) => <OriginPill job={row.original} />,
      },
      {
        id: "safety",
        header: "Safety Decision",
        accessorFn: (j) => j._safetyDecision ?? "",
        cell: ({ row }) => (
          <SafetyDecisionBadge
            decision={row.original._safetyDecision}
            matchedRules={row.original._matchedRules}
          />
        ),
      },
      {
        id: "attempts",
        header: "Attempts",
        accessorFn: (j) => j.attempts ?? 0,
        meta: { align: "center" },
        cell: ({ row }) => (
          <span className="font-mono text-xs text-muted-foreground">
            {row.original.attempts ?? 0}
          </span>
        ),
      },
      {
        id: "updatedAt",
        header: "Updated",
        accessorFn: (j) => new Date(j.updatedAt ?? 0).getTime(),
        meta: { align: "right" },
        cell: ({ row }) => (
          <span className="text-xs text-muted-foreground font-mono">
            {row.original.updatedAt
              ? formatRelativeTime(new Date(row.original.updatedAt).toISOString())
              : "—"}
          </span>
        ),
      },
      // Actions column — visible only in DLQ mode. Each row's Replay /
      // Drop buttons set pending-action state, which opens the matching
      // ConfirmDialog. Replay is idempotency-confirmed; Drop is type-to-confirm
      // (destructive). data-row-action stops the row click from triggering
      // navigation when the user clicks an action button (DataTable's
      // isInteractiveTarget helper checks for this attribute).
      ...(isDlqMode
        ? [
            {
              id: "actions",
              header: "Actions",
              enableSorting: false,
              cell: ({ row }: { row: { original: EnrichedJob } }) => (
                <div
                  className="flex items-center justify-end gap-1"
                  data-row-action
                >
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={(e) => {
                      e.stopPropagation();
                      setPendingReplay(row.original.id);
                    }}
                    title="Replay this DLQ entry"
                    aria-label={`Replay DLQ entry ${row.original.id}`}
                  >
                    <RotateCcw className="h-3.5 w-3.5 mr-1" />
                    Replay
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-destructive hover:text-destructive"
                    onClick={(e) => {
                      e.stopPropagation();
                      setPendingDrop(row.original.id);
                    }}
                    title="Drop this DLQ entry permanently"
                    aria-label={`Drop DLQ entry ${row.original.id}`}
                  >
                    <Trash2 className="h-3.5 w-3.5 mr-1" />
                    Drop
                  </Button>
                </div>
              ),
              meta: { align: "right" },
            } satisfies ColumnDef<EnrichedJob>,
          ]
        : []),
    ],
    [isDlqMode],
  );

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

      {/* Advanced Filters */}
      <div className="space-y-4">
        <JobFiltersBar onChange={setJobFilters} />

        <div className="flex flex-wrap items-center gap-3">
          <Input
            type="text"
            placeholder="Search by ID, topic, or trace..."
            value={search ?? ""}
            onChange={(e) => void setSearch(e.target.value)}
            icon={<Search className="h-3.5 w-3.5" />}
            className="h-9 max-w-sm flex-1 text-sm"
          />
          <Tabs
            tabs={tabs}
            activeTab={activeTab}
            onChange={(tab) => void setActiveTab(tab as (typeof STATUS_FILTERS)[number])}
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
            onChange={(tab) => void setSafetyFilter(tab as (typeof SAFETY_FILTERS)[number])}
            variant="segmented"
            ariaLabel="Safety decision filters"
            className="w-full"
          />
        </div>
      </div>

      {/* DLQ banner — renders only when ?status=dlq. Copy explicitly says
          "failed terminally" so users don't think these are still being retried. */}
      {isDlqMode && (
        <InfoBanner variant="warning" title="Dead-letter queue">
          Jobs that failed terminally and were dropped from live processing.
          {safetyFilter !== "all" && (
            <span className="ml-2 text-xs opacity-80">
              · Safety filter does not apply to DLQ entries (no decision was
              recorded before drop).
            </span>
          )}
        </InfoBanner>
      )}

      {/* Jobs Table — primitives/DataTable virtualizes when row count >100. */}
      {isLoading ? (
        <div className="instrument-card">
          <SkeletonTable rows={8} />
        </div>
      ) : (
        <div className="instrument-card overflow-hidden">
          <DataTable
            columns={columns}
            data={filtered}
            decisionAccessor={decisionAccessor}
            onRowClick={(job) => navigate(`/jobs/${job.id}`)}
            initialSorting={[{ id: "updatedAt", desc: true }]}
            emptyState={
              <EmptyState
                icon={<ListChecks className="w-5 h-5" />}
                title="No jobs found"
                description={
                  search ||
                  activeTab !== "all" ||
                  safetyFilter !== "all" ||
                  Object.keys(jobFilters).length > 0
                    ? "Try adjusting your search or filters"
                    : "No jobs have been submitted yet"
                }
                action={
                  search ||
                  activeTab !== "all" ||
                  safetyFilter !== "all" ||
                  Object.keys(jobFilters).length > 0 ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        void setSearch(null);
                        void setActiveTab(null);
                        void setSafetyFilter(null);
                        setJobFilters({});
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
            }
          />
          <div className="flex items-center justify-between px-5 py-2 text-xs font-mono text-muted-foreground border-t border-border">
            <span>
              {filtered.length} of {enrichedJobs.length} jobs
            </span>
          </div>
        </div>
      )}

      <SubmitJobDialog open={showSubmit} onClose={() => setShowSubmit(false)} />

      {/* Replay confirm — idempotency guard so a stray click does not
          double-replay. ConfirmDialog primitive already handles focus + ESC. */}
      <ConfirmDialog
        open={pendingReplay !== null}
        onCancel={() => setPendingReplay(null)}
        onClose={() => setPendingReplay(null)}
        onConfirm={() => {
          if (!pendingReplay) return;
          retryMutation.mutate({ id: pendingReplay });
          setPendingReplay(null);
        }}
        title="Replay DLQ entry?"
        description="This re-enqueues the failed job. The action is not reversible from here once it succeeds."
        confirmLabel="Replay"
        variant="default"
        isPending={retryMutation.isPending}
      />

      {/* Drop confirm — destructive, requires the user to TYPE the entry id
          before the Confirm button enables (ConfirmDialog primitive's
          confirmText prop). Catches accidental clicks. */}
      <ConfirmDialog
        open={pendingDrop !== null}
        onCancel={() => setPendingDrop(null)}
        onClose={() => setPendingDrop(null)}
        onConfirm={() => {
          if (!pendingDrop) return;
          dropMutation.mutate(pendingDrop);
          setPendingDrop(null);
        }}
        title="Drop DLQ entry?"
        description="This permanently removes the entry. There is no recovery — the failed job will be invisible to operators after this confirms."
        confirmLabel="Drop"
        variant="destructive"
        confirmText={pendingDrop ?? undefined}
        isPending={dropMutation.isPending}
      />
    </div>
  );
}
