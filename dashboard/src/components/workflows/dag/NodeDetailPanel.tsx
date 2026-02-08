import { useState } from "react";
import { Link } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import {
  Briefcase,
  UserCheck,
  Clock,
  GitBranch,
  Bell,
  GitFork,
  ChevronDown,
  ChevronRight,
  X,
  Loader,
} from "lucide-react";
import { Drawer } from "../../ui/Drawer";
import { Badge } from "../../ui/Badge";
import { Button } from "../../ui/Button";
import { Card } from "../../ui/Card";
import { Textarea } from "../../ui/Textarea";
import { RunStatusBadge } from "../../StatusBadge";
import { useApproveJob, useRejectJob, useApproveStep } from "../../../hooks/useApprovals";
import type { WorkflowStep, WorkflowRun } from "../../../api/types";
import { cn } from "../../../lib/utils";

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface NodeDetailPanelProps {
  step: WorkflowStep | null;
  run?: WorkflowRun | null;
  onClose: () => void;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function truncate(str: string, max: number): string {
  return str.length > max ? str.slice(0, max) + "\u2026" : str;
}

function formatDuration(ms: number): string {
  const secs = Math.round(ms / 1000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  const rem = secs % 60;
  return rem > 0 ? `${mins}m ${rem}s` : `${mins}m`;
}

function formatDate(iso?: string): string {
  if (!iso) return "\u2014";
  return new Date(iso).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function safeJsonStr(val: unknown, maxLen: number): string {
  try {
    const str = JSON.stringify(val, null, 2);
    return str.length > maxLen ? str.slice(0, maxLen) + "\n\u2026 (truncated)" : str;
  } catch {
    return String(val);
  }
}

const JOB_ID_PATTERN = /^[a-zA-Z0-9_.-]+$/;

// ---------------------------------------------------------------------------
// Step type icons
// ---------------------------------------------------------------------------

const STEP_ICONS: Record<string, React.ReactNode> = {
  job: <Briefcase className="h-4 w-4" />,
  approval: <UserCheck className="h-4 w-4" />,
  delay: <Clock className="h-4 w-4" />,
  condition: <GitBranch className="h-4 w-4" />,
  notify: <Bell className="h-4 w-4" />,
  "fan-out": <GitFork className="h-4 w-4" />,
};

// ---------------------------------------------------------------------------
// Collapsible JSON section
// ---------------------------------------------------------------------------

function CollapsibleJson({ label, data }: { label: string; data: unknown }) {
  const [open, setOpen] = useState(false);

  if (data == null) return null;

  return (
    <div className="rounded-xl border border-border">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex w-full items-center gap-2 px-3 py-2 text-xs font-semibold text-muted hover:text-ink transition"
      >
        {open ? (
          <ChevronDown className="h-3.5 w-3.5" />
        ) : (
          <ChevronRight className="h-3.5 w-3.5" />
        )}
        {label}
      </button>
      {open && (
        <pre className="max-h-[300px] overflow-auto border-t border-border bg-surface2/30 px-3 py-2 text-[11px] text-ink">
          {safeJsonStr(data, 5000)}
        </pre>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Section wrapper
// ---------------------------------------------------------------------------

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <h4 className="text-[10px] font-semibold uppercase tracking-wider text-muted">
        {label}
      </h4>
      {children}
    </div>
  );
}

function InfoRow({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between text-xs">
      <span className="text-muted">{label}</span>
      <span className="font-medium text-ink">{value}</span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Step type detail renderers
// ---------------------------------------------------------------------------

function JobDetail({
  step,
  runStep,
}: {
  step: WorkflowStep;
  runStep?: WorkflowStep;
}) {
  const config = step.config ?? {};
  const output = runStep?.output ?? {};
  const safetyDecision = output.safetyDecision as
    | { type: string; reason?: string; matchedRule?: string; evalTimeMs?: number }
    | undefined;
  const jobId = (config.jobId as string) ?? (output.jobId as string) ?? "";
  const workerId = (output.workerId as string) ?? "";
  const request = config.payload ?? config.request ?? config;
  const result = output.result ?? output;
  const error = runStep?.error;

  let duration: number | undefined;
  if (runStep?.startedAt && runStep?.completedAt) {
    duration =
      new Date(runStep.completedAt).getTime() -
      new Date(runStep.startedAt).getTime();
  }

  return (
    <div className="space-y-4">
      {/* Status + Duration */}
      <Section label="Execution">
        <div className="flex items-center justify-between">
          <RunStatusBadge status={runStep?.status} />
          {duration != null && (
            <span className="text-xs text-muted">{formatDuration(duration)}</span>
          )}
        </div>
        {runStep?.startedAt && (
          <InfoRow label="Started" value={formatDate(runStep.startedAt)} />
        )}
        {runStep?.completedAt && (
          <InfoRow label="Completed" value={formatDate(runStep.completedAt)} />
        )}
      </Section>

      {/* Job ID link */}
      {jobId && (
        <Section label="Job">
          {JOB_ID_PATTERN.test(jobId) ? (
            <Link
              to={`/jobs/${jobId}`}
              className="text-xs font-mono text-accent hover:underline"
            >
              {jobId}
            </Link>
          ) : (
            <span className="text-xs font-mono text-ink">{truncate(jobId, 60)}</span>
          )}
          {workerId && <InfoRow label="Worker" value={truncate(workerId, 40)} />}
        </Section>
      )}

      {/* Safety decision */}
      {safetyDecision && (
        <Section label="Safety Decision">
          <div className="flex items-center gap-2">
            <Badge
              variant={
                safetyDecision.type === "allow"
                  ? "success"
                  : safetyDecision.type === "deny"
                    ? "danger"
                    : safetyDecision.type === "require_approval"
                      ? "warning"
                      : "info"
              }
            >
              {safetyDecision.type}
            </Badge>
            {safetyDecision.matchedRule && (
              <span className="text-[10px] text-muted">
                rule: {safetyDecision.matchedRule}
              </span>
            )}
          </div>
          {safetyDecision.reason && (
            <p className="text-xs text-muted">{truncate(safetyDecision.reason, 200)}</p>
          )}
          {safetyDecision.evalTimeMs != null && (
            <InfoRow label="Eval time" value={`${safetyDecision.evalTimeMs}ms`} />
          )}
        </Section>
      )}

      {/* Error */}
      {error && (
        <Card className="border-danger/40 bg-danger/5">
          <p className="text-xs font-semibold text-danger">Error</p>
          <p className="mt-1 text-xs text-ink">{truncate(error, 500)}</p>
        </Card>
      )}

      {/* Payloads */}
      <CollapsibleJson label="Request Payload" data={request} />
      <CollapsibleJson label="Result Payload" data={result} />
    </div>
  );
}

function ApprovalDetail({
  step,
  runStep,
  run,
}: {
  step: WorkflowStep;
  runStep?: WorkflowStep;
  run?: WorkflowRun | null;
}) {
  const queryClient = useQueryClient();
  const approveJob = useApproveJob();
  const rejectJob = useRejectJob();
  const approveStep = useApproveStep();

  const [comment, setComment] = useState("");
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionSuccess, setActionSuccess] = useState<string | null>(null);

  const output = runStep?.output ?? {};
  const config = runStep?.config ?? step.config ?? {};
  const approvalStatus = (output.status as string) ?? runStep?.status ?? "pending";
  const actor = (output.actor as string) ?? "";
  const existingComment = (output.comment as string) ?? "";
  const resolvedAt = (output.resolvedAt as string) ?? runStep?.completedAt;

  // Extract jobId for job-based approval
  const jobId =
    (config.jobId as string) ??
    (output.jobId as string) ??
    (output.job_id as string) ??
    "";

  let pendingDuration: number | undefined;
  if (runStep?.startedAt) {
    const end = resolvedAt ? new Date(resolvedAt).getTime() : Date.now();
    pendingDuration = end - new Date(runStep.startedAt).getTime();
  }

  const isWaiting =
    runStep?.status === "waiting" || runStep?.status === "blocked" || runStep?.status === "pending";

  const isPending = approveJob.isPending || rejectJob.isPending || approveStep.isPending;

  const invalidateRuns = () => {
    queryClient.invalidateQueries({ queryKey: ["workflow-runs"] });
    if (run?.workflowId) {
      queryClient.invalidateQueries({ queryKey: ["workflow-runs", run.workflowId] });
    }
    if (run?.id) {
      queryClient.invalidateQueries({ queryKey: ["workflow-run", run.id] });
    }
  };

  const handleApprove = () => {
    setActionError(null);
    setActionSuccess(null);

    if (jobId && JOB_ID_PATTERN.test(jobId)) {
      approveJob.mutate(
        { id: jobId, comment: comment.trim() || undefined },
        {
          onSuccess: () => {
            setActionSuccess("Approved");
            setComment("");
            invalidateRuns();
          },
          onError: (err) => setActionError(err.message),
        },
      );
    } else if (run?.workflowId && run?.id) {
      approveStep.mutate(
        { workflowId: run.workflowId, runId: run.id, stepId: step.id, approved: true },
        {
          onSuccess: () => {
            setActionSuccess("Approved");
            setComment("");
            invalidateRuns();
          },
          onError: (err) => setActionError(err.message),
        },
      );
    }
  };

  const handleReject = () => {
    setActionError(null);
    setActionSuccess(null);
    const reason = comment.trim() || "Rejected from DAG panel";

    if (jobId && JOB_ID_PATTERN.test(jobId)) {
      rejectJob.mutate(
        { id: jobId, reason, comment: comment.trim() || undefined },
        {
          onSuccess: () => {
            setActionSuccess("Rejected");
            setComment("");
            invalidateRuns();
          },
          onError: (err) => setActionError(err.message),
        },
      );
    } else if (run?.workflowId && run?.id) {
      approveStep.mutate(
        { workflowId: run.workflowId, runId: run.id, stepId: step.id, approved: false },
        {
          onSuccess: () => {
            setActionSuccess("Rejected");
            setComment("");
            invalidateRuns();
          },
          onError: (err) => setActionError(err.message),
        },
      );
    }
  };

  return (
    <div className="space-y-4">
      <Section label="Approval Status">
        <RunStatusBadge status={runStep?.status} />
        <InfoRow
          label="Status"
          value={
            <Badge
              variant={
                approvalStatus === "approved"
                  ? "success"
                  : approvalStatus === "rejected"
                    ? "danger"
                    : "warning"
              }
            >
              {approvalStatus}
            </Badge>
          }
        />
      </Section>

      {actor && (
        <Section label="Resolution">
          <InfoRow label="Actor" value={truncate(actor, 60)} />
          {resolvedAt && <InfoRow label="Resolved at" value={formatDate(resolvedAt)} />}
          {existingComment && (
            <p className="text-xs text-muted italic">{truncate(existingComment, 300)}</p>
          )}
        </Section>
      )}

      {pendingDuration != null && (
        <Section label="Timing">
          <InfoRow
            label={isWaiting ? "Waiting for" : "Was pending for"}
            value={formatDuration(pendingDuration)}
          />
        </Section>
      )}

      {/* In-context approve / reject actions */}
      {isWaiting && (
        <div className="space-y-3 rounded-xl border border-border p-3">
          <Textarea
            placeholder="Add a comment (optional)..."
            value={comment}
            onChange={(e) => setComment(e.target.value)}
            rows={2}
            className="text-xs"
            disabled={isPending}
          />
          <div className="flex gap-2">
            <Button
              variant="primary"
              size="sm"
              type="button"
              className="bg-green-600 hover:bg-green-700"
              onClick={handleApprove}
              disabled={isPending}
            >
              {approveJob.isPending || approveStep.isPending ? (
                <Loader className="h-3 w-3 animate-spin" />
              ) : null}
              Approve
            </Button>
            <Button
              variant="danger"
              size="sm"
              type="button"
              onClick={handleReject}
              disabled={isPending}
            >
              {rejectJob.isPending ? (
                <Loader className="h-3 w-3 animate-spin" />
              ) : null}
              Reject
            </Button>
          </div>

          {actionSuccess && (
            <p className="text-xs font-medium text-success">{actionSuccess}</p>
          )}
          {actionError && (
            <p className="text-xs font-medium text-danger">{actionError}</p>
          )}
        </div>
      )}
    </div>
  );
}

function ConditionDetail({
  step,
  runStep,
}: {
  step: WorkflowStep;
  runStep?: WorkflowStep;
}) {
  const config = step.config ?? {};
  const expression = step.condition ?? (config.expression as string) ?? (config.condition as string) ?? "";
  const output = runStep?.output ?? {};
  const result = output.result as boolean | undefined;
  const branchTaken = (output.branch as string) ?? "";

  return (
    <div className="space-y-4">
      <Section label="Execution">
        <RunStatusBadge status={runStep?.status} />
      </Section>

      {expression && (
        <Section label="Expression">
          <pre className="rounded-lg bg-surface2/40 px-3 py-2 text-xs font-mono text-ink">
            {truncate(expression, 500)}
          </pre>
        </Section>
      )}

      {result !== undefined && (
        <Section label="Result">
          <Badge variant={result ? "success" : "danger"}>
            {String(result)}
          </Badge>
        </Section>
      )}

      {branchTaken && (
        <Section label="Branch Taken">
          <span className="text-xs font-medium text-ink">{truncate(branchTaken, 60)}</span>
        </Section>
      )}
    </div>
  );
}

function DelayDetail({
  step,
  runStep,
}: {
  step: WorkflowStep;
  runStep?: WorkflowStep;
}) {
  const config = step.config ?? {};
  const delayMs =
    (step.delay_sec != null ? step.delay_sec * 1000 : undefined) ??
    (config.duration as number) ??
    (config.delayMs as number) ??
    (config.delay as number);

  let elapsed: number | undefined;
  if (runStep?.startedAt) {
    const end = runStep.completedAt
      ? new Date(runStep.completedAt).getTime()
      : Date.now();
    elapsed = end - new Date(runStep.startedAt).getTime();
  }

  const isRunning =
    runStep?.status === "running" || runStep?.status === "in_progress";

  return (
    <div className="space-y-4">
      <Section label="Execution">
        <RunStatusBadge status={runStep?.status} />
      </Section>

      {delayMs != null && (
        <Section label="Configured Delay">
          <span className="text-xs font-medium text-ink">{formatDuration(delayMs)}</span>
        </Section>
      )}

      {elapsed != null && (
        <Section label={isRunning ? "Time Elapsed" : "Total Wait"}>
          <span className="text-xs font-medium text-ink">{formatDuration(elapsed)}</span>
          {isRunning && delayMs != null && elapsed < delayMs && (
            <span className="ml-2 text-[10px] text-muted">
              ({formatDuration(delayMs - elapsed)} remaining)
            </span>
          )}
        </Section>
      )}
    </div>
  );
}

function FanOutDetail({
  step,
  runStep,
  run,
}: {
  step: WorkflowStep;
  runStep?: WorkflowStep;
  run?: WorkflowRun | null;
}) {
  const config = step.config ?? {};
  const parallelism = step.max_parallel ?? (config.parallelism as number) ?? (config.branches as number);
  const forEachExpr = step.for_each ?? (config.forEach as string) ?? "";

  // Find child steps (steps that depend on this fan-out)
  const childSteps = (run?.steps ?? []).filter((rs) =>
    (rs.depends_on ?? rs.dependsOn)?.includes(step.id),
  );

  return (
    <div className="space-y-4">
      <Section label="Execution">
        <RunStatusBadge status={runStep?.status} />
      </Section>

      {parallelism != null && (
        <InfoRow label="Parallelism" value={parallelism} />
      )}

      {forEachExpr && (
        <Section label="ForEach Expression">
          <pre className="rounded-lg bg-surface2/40 px-3 py-2 text-xs font-mono text-ink">
            {truncate(forEachExpr, 200)}
          </pre>
        </Section>
      )}

      {childSteps.length > 0 && (
        <Section label={`Branches (${childSteps.length})`}>
          <div className="space-y-1">
            {childSteps.map((cs) => (
              <div
                key={cs.id}
                className="flex items-center justify-between rounded-lg border border-border px-3 py-1.5"
              >
                <span className="text-xs text-ink">{cs.name || cs.id}</span>
                <RunStatusBadge status={cs.status} />
              </div>
            ))}
          </div>
        </Section>
      )}
    </div>
  );
}

function GenericDetail({ runStep }: { runStep?: WorkflowStep }) {
  return (
    <div className="space-y-4">
      <Section label="Execution">
        <RunStatusBadge status={runStep?.status} />
      </Section>
      {runStep?.output && (
        <CollapsibleJson label="Output" data={runStep.output} />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// NodeDetailPanel
// ---------------------------------------------------------------------------

export function NodeDetailPanel({
  step,
  run,
  onClose,
}: NodeDetailPanelProps) {
  // Find matching run step
  const runStep = step && run?.steps
    ? run.steps.find((rs) => rs.id === step.id)
    : undefined;

  const stepIcon = step ? (STEP_ICONS[step.type] ?? STEP_ICONS.job) : null;

  return (
    <Drawer open={step !== null} onClose={onClose} size="sm">
      {step && (
        <div className="space-y-6">
          {/* Header */}
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-3 min-w-0">
              <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-surface2 text-muted">
                {stepIcon}
              </div>
              <div className="min-w-0">
                <h3
                  className="truncate font-display text-base font-semibold text-ink"
                  title={step.name || step.id}
                >
                  {truncate(step.name || step.id, 60)}
                </h3>
                <Badge variant="info" className="text-[10px]">
                  {step.type}
                </Badge>
              </div>
            </div>
            <button
              type="button"
              onClick={onClose}
              className="rounded-full p-1.5 hover:bg-surface2 transition"
            >
              <X className="h-4 w-4 text-muted" />
            </button>
          </div>

          {/* Step-type-specific content */}
          {step.type === "job" && (
            <JobDetail step={step} runStep={runStep} />
          )}
          {step.type === "approval" && (
            <ApprovalDetail
              step={step}
              runStep={runStep}
              run={run}
            />
          )}
          {step.type === "condition" && (
            <ConditionDetail step={step} runStep={runStep} />
          )}
          {step.type === "delay" && (
            <DelayDetail step={step} runStep={runStep} />
          )}
          {(step.type === "fan-out" || step.type === "fanout") && (
            <FanOutDetail step={step} runStep={runStep} run={run} />
          )}
          {step.type === "notify" && (
            <GenericDetail runStep={runStep} />
          )}
          {!["job", "approval", "condition", "delay", "fan-out", "fanout", "notify"].includes(step.type) && (
            <GenericDetail runStep={runStep} />
          )}
        </div>
      )}
    </Drawer>
  );
}
