import { useState, useCallback, useEffect, useMemo } from "react";
import { useParams, useNavigate } from "react-router-dom";
import {
  Play,
  Pencil,
  Trash2,
  Copy,
  Check,
  Loader,
  Code,
} from "lucide-react";
import {
  useWorkflow,
  useRuns,
  useStartRun,
  useDeleteWorkflow,
} from "../hooks/useWorkflows";
import { RequireRole } from "../components/RequireRole";
import { useRunStream } from "../hooks/useRunStream";
import { RunDAG, NodeDetailPanel } from "../components/workflows/dag";
import { RunStatusBadge } from "../components/StatusBadge";
import { Badge } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { ConfirmDialog } from "../components/ui/ConfirmDialog";
import { cn } from "../lib/utils";
import type { WorkflowRun, WorkflowStep } from "../api/types";
import { usePageTitle } from "../hooks/usePageTitle";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function timeAgo(iso?: string): string {
  if (!iso) return "\u2014";
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1_000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

function formatDuration(ms?: number): string {
  if (ms == null) return "\u2014";
  const secs = Math.round(ms / 1000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  const rem = secs % 60;
  return rem > 0 ? `${mins}m ${rem}s` : `${mins}m`;
}

function truncate(str: string, max: number): string {
  return str.length > max ? str.slice(0, max) + "\u2026" : str;
}

// ---------------------------------------------------------------------------
// Steps mini-bar
// ---------------------------------------------------------------------------

const STEP_STATUS_COLORS: Record<string, string> = {
  succeeded: "bg-green-500",
  completed: "bg-green-500",
  running: "bg-blue-500",
  in_progress: "bg-blue-500",
  failed: "bg-red-500",
  timed_out: "bg-red-500",
  waiting: "bg-amber-500",
  blocked: "bg-amber-500",
  pending: "bg-gray-300",
  queued: "bg-gray-300",
  cancelled: "bg-gray-400",
};

function StepsMiniBar({ steps }: { steps: WorkflowStep[] }) {
  if (steps.length === 0) return <span className="text-xs text-muted">\u2014</span>;
  const total = steps.length;

  return (
    <div className="flex items-center gap-1">
      <div className="flex h-2 w-16 overflow-hidden rounded-full">
        {steps.map((s, i) => (
          <div
            key={s.id ?? i}
            className={cn(
              "h-full",
              STEP_STATUS_COLORS[s.status ?? ""] ?? "bg-gray-200",
            )}
            style={{ width: `${100 / total}%` }}
          />
        ))}
      </div>
      <span className="text-[10px] text-muted">
        {steps.filter((s) => s.status === "succeeded" || s.status === "completed").length}/{total}
      </span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// WorkflowDetailPage
// ---------------------------------------------------------------------------

export default function WorkflowDetailPage() {
  const { id, runId: urlRunId } = useParams<{ id: string; runId?: string }>();
  usePageTitle(id ? `Workflow ${id.slice(0, 8)}` : "Workflow");
  const navigate = useNavigate();

  // Data
  const { data: workflow, isLoading, isError } = useWorkflow(id);
  const { data: runs } = useRuns(id, { limit: 50 });
  const startRun = useStartRun();
  const deleteWorkflow = useDeleteWorkflow();

  // UI state
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [selectedStepId, setSelectedStepId] = useState<string | null>(null);
  const [showDefinition, setShowDefinition] = useState(false);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [copied, setCopied] = useState(false);

  // Live WebSocket updates for selected run's DAG
  useRunStream(selectedRunId);

  // Auto-select run: URL param first, then most recent
  useEffect(() => {
    if (!runs || runs.length === 0) {
      setSelectedRunId(null);
      return;
    }
    if (urlRunId && runs.some((r) => r.id === urlRunId)) {
      setSelectedRunId(urlRunId);
    } else if (!selectedRunId || !runs.some((r) => r.id === selectedRunId)) {
      setSelectedRunId(runs[0].id);
    }
  }, [runs, urlRunId, selectedRunId]);

  const selectedRun = useMemo(
    () => runs?.find((r) => r.id === selectedRunId) ?? null,
    [runs, selectedRunId],
  );

  // Find step definition by ID for the detail panel
  const selectedStep = useMemo<WorkflowStep | null>(() => {
    if (!selectedStepId || !workflow) return null;
    return workflow.steps.find((s) => s.id === selectedStepId) ?? null;
  }, [selectedStepId, workflow]);

  const handleSelectRun = useCallback(
    (run: WorkflowRun) => {
      setSelectedRunId(run.id);
      if (id) {
        navigate(`/workflows/${id}/runs/${run.id}`, { replace: true });
      }
    },
    [id, navigate],
  );

  const handleStartRun = useCallback(() => {
    if (!id) return;
    startRun.mutate({ workflowId: id });
  }, [id, startRun]);

  const handleDelete = useCallback(() => {
    if (!id) return;
    deleteWorkflow.mutate(id, {
      onSuccess: () => navigate("/workflows"),
    });
  }, [id, deleteWorkflow, navigate]);

  const handleCopyDefinition = useCallback(async () => {
    if (!workflow) return;
    await navigator.clipboard.writeText(JSON.stringify(workflow, null, 2));
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  }, [workflow]);

  // Loading / error states
  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-16 text-sm text-muted">
        <Loader className="mr-2 h-4 w-4 animate-spin" />
        Loading workflow...
      </div>
    );
  }

  if (isError || !workflow) {
    return (
      <div className="py-16 text-center text-sm text-danger">
        Failed to load workflow.
      </div>
    );
  }

  const runDuration = (run: WorkflowRun): number | undefined => {
    if (run.duration) return run.duration;
    if (run.startedAt && run.completedAt) {
      return new Date(run.completedAt).getTime() - new Date(run.startedAt).getTime();
    }
    return undefined;
  };

  return (
    <div className="space-y-6">
      {/* ================================================================= */}
      {/* Header                                                            */}
      {/* ================================================================= */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="font-display text-2xl font-semibold text-ink">
            {truncate(workflow.name, 80)}
          </h2>
          <p className="text-sm text-muted">
            {workflow.steps.length} step{workflow.steps.length !== 1 ? "s" : ""}
            {workflow.description && <> &middot; {truncate(workflow.description, 100)}</>}
            {workflow.triggerType && (
              <> &middot; <Badge variant="info">{workflow.triggerType}</Badge></>
            )}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={handleStartRun}
            disabled={startRun.isPending}
          >
            <Play className="h-4 w-4" />
            {startRun.isPending ? "Starting\u2026" : "Run Now"}
          </Button>
          <Button
            variant="outline"
            size="sm"
            type="button"
            onClick={() => navigate(`/workflows/${id}/edit`)}
          >
            <Pencil className="h-4 w-4" />
            Edit Workflow
          </Button>
          <Button
            variant="outline"
            size="sm"
            type="button"
            onClick={() => setShowDefinition(!showDefinition)}
          >
            <Code className="h-4 w-4" />
            {showDefinition ? "Hide Definition" : "Edit Definition"}
          </Button>
          <RequireRole roles={["admin"]}>
          <Button
            variant="ghost"
            className="text-danger hover:bg-danger/10"
            type="button"
            onClick={() => setShowDeleteConfirm(true)}
          >
            <Trash2 className="h-4 w-4" />
          </Button>
          </RequireRole>
        </div>
      </div>

      {/* ================================================================= */}
      {/* Section A — Run History                                           */}
      {/* ================================================================= */}
      <div>
        <h3 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted">
          Run History
        </h3>
        {!runs || runs.length === 0 ? (
          <Card>
            <p className="py-4 text-center text-sm text-muted">
              No runs yet. Click "Run Now" to trigger the first execution.
            </p>
          </Card>
        ) : (
          <div className="overflow-x-auto rounded-2xl border border-border">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border bg-surface2/60 text-left text-xs uppercase tracking-wider text-muted">
                  <th className="px-4 py-3">Run ID</th>
                  <th className="px-4 py-3">Status</th>
                  <th className="px-4 py-3">Started</th>
                  <th className="px-4 py-3">Duration</th>
                  <th className="px-4 py-3">Steps</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {runs.map((run) => {
                  const isSelected = run.id === selectedRunId;
                  return (
                    <tr
                      key={run.id}
                      onClick={() => handleSelectRun(run)}
                      className={cn(
                        "cursor-pointer transition-colors",
                        isSelected
                          ? "border-l-2 border-l-accent bg-accent/5"
                          : "hover:bg-surface2/40",
                      )}
                    >
                      <td className="px-4 py-3 font-mono text-xs text-ink">
                        {run.id.slice(0, 8)}
                      </td>
                      <td className="px-4 py-3">
                        <RunStatusBadge status={run.status} />
                      </td>
                      <td className="px-4 py-3 text-xs text-muted">
                        {timeAgo(run.startedAt ?? run.createdAt)}
                      </td>
                      <td className="px-4 py-3 text-xs text-muted">
                        {formatDuration(runDuration(run))}
                      </td>
                      <td className="px-4 py-3">
                        <StepsMiniBar steps={run.steps ?? []} />
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* ================================================================= */}
      {/* Section B — DAG Visualizer                                        */}
      {/* ================================================================= */}
      <div>
        <h3 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted">
          DAG Visualizer
          {selectedRun && (
            <span className="ml-2 text-ink">
              &middot; Run {selectedRun.id.slice(0, 8)}
            </span>
          )}
        </h3>
        <div className="min-h-[400px] overflow-hidden rounded-2xl border border-border bg-white">
          <RunDAG
            workflow={workflow}
            run={selectedRun}
            onNodeClick={(stepId) => setSelectedStepId(stepId)}
            className="h-[400px]"
          />
          {!runs?.length && (
            <div className="flex items-center justify-center py-8 text-sm text-muted">
              No runs yet — click "Run Now" to see the DAG in action.
            </div>
          )}
        </div>
      </div>

      {/* ================================================================= */}
      {/* Section C — Workflow Definition (collapsed by default)            */}
      {/* ================================================================= */}
      {showDefinition && (
        <div>
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted">
              Workflow Definition
            </h3>
            <Button
              variant="ghost"
              size="sm"
              type="button"
              onClick={handleCopyDefinition}
            >
              {copied ? (
                <Check className="h-3.5 w-3.5 text-success" />
              ) : (
                <Copy className="h-3.5 w-3.5" />
              )}
              {copied ? "Copied" : "Copy"}
            </Button>
          </div>
          <div className="max-h-[500px] overflow-auto rounded-2xl border border-border bg-surface2/30 p-4">
            <pre className="text-xs font-mono text-ink whitespace-pre-wrap">
              {JSON.stringify(workflow, null, 2)}
            </pre>
          </div>
        </div>
      )}

      {/* ================================================================= */}
      {/* NodeDetailPanel (slides from right on node click)                 */}
      {/* ================================================================= */}
      <NodeDetailPanel
        step={selectedStep}
        run={selectedRun}
        onClose={() => setSelectedStepId(null)}
      />

      {/* Delete confirm */}
      <ConfirmDialog
        open={showDeleteConfirm}
        title="Delete Workflow"
        message={`Are you sure you want to delete "${workflow.name}"? This action cannot be undone.`}
        confirmLabel="Delete"
        confirmVariant="danger"
        isPending={deleteWorkflow.isPending}
        onConfirm={handleDelete}
        onCancel={() => setShowDeleteConfirm(false)}
      />
    </div>
  );
}
