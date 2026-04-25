import { useState } from "react";
import { Link } from "react-router-dom";
import { logger } from "../../../lib/logger";
import { formatDuration, truncate } from "@/lib/utils";
import { useQueryClient } from "@tanstack/react-query";
import {
  Briefcase,
  UserCheck,
  Clock,
  GitBranch,
  GitMerge,
  Bell,
  GitFork,
  Layers,
  Repeat,
  Workflow,
  Code,
  Database,
  Globe,
  AlertTriangle,
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
import { useApproveJob, useRejectJob } from "../../../hooks/useApprovals";
import type { WorkflowStep, WorkflowRun } from "../../../api/types";
import { cn } from "../../../lib/utils";
import { friendlyError } from "../../../lib/friendlyError";

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface NodeDetailPanelProps {
  step: WorkflowStep | null;
  run?: WorkflowRun | null;
  onClose: () => void;
  /** When true, render content directly without the Drawer overlay wrapper. */
  inline?: boolean;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------


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
    logger.debug("node-detail", "JSON.stringify failed in safeJsonStr");
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
  switch: <GitMerge className="h-4 w-4" />,
  notify: <Bell className="h-4 w-4" />,
  "fan-out": <GitFork className="h-4 w-4" />,
  parallel: <Layers className="h-4 w-4" />,
  loop: <Repeat className="h-4 w-4" />,
  "sub-workflow": <Workflow className="h-4 w-4" />,
  subworkflow: <Workflow className="h-4 w-4" />,
  transform: <Code className="h-4 w-4" />,
  storage: <Database className="h-4 w-4" />,
  http: <Globe className="h-4 w-4" />,
  "error-trigger": <AlertTriangle className="h-4 w-4" />,
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
        className="flex w-full items-center gap-2 px-3 py-2 text-xs font-semibold text-muted-foreground hover:text-ink transition"
      >
        {open ? (
          <ChevronDown className="h-3.5 w-3.5" />
        ) : (
          <ChevronRight className="h-3.5 w-3.5" />
        )}
        {label}
      </button>
      {open && (
        <pre className="max-h-[300px] overflow-auto border-t border-border bg-surface2/30 px-3 py-2 text-xs text-ink">
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
      <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        {label}
      </h4>
      {children}
    </div>
  );
}

function InfoRow({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between text-xs">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-medium text-ink">{value}</span>
    </div>
  );
}

function parseLoopChildIndex(parentStepId: string, childStepId: string): number | null {
  const escapedParent = parentStepId.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`^${escapedParent}\\[(\\d+)\\]$`).exec(childStepId);
  if (!match) return null;
  const parsed = Number.parseInt(match[1], 10);
  return Number.isFinite(parsed) ? parsed : null;
}

type SwitchCaseView = {
  matchValue: string;
  stepId: string;
};

function parseSwitchCasesForView(raw: unknown): SwitchCaseView[] {
  if (Array.isArray(raw)) {
    return raw
      .map((entry) => {
        if (!entry || typeof entry !== "object") return null;
        const record = entry as Record<string, unknown>;
        const stepRaw = record.next ?? record.step ?? record.target ?? record.step_id;
        const stepId = typeof stepRaw === "string" ? stepRaw.trim() : "";
        if (!stepId) return null;
        const matchRaw = record.match ?? record.when ?? record.value;
        return {
          matchValue: matchRaw == null ? "" : String(matchRaw),
          stepId,
        };
      })
      .filter((entry): entry is SwitchCaseView => entry !== null);
  }
  if (raw && typeof raw === "object") {
    return Object.entries(raw as Record<string, unknown>)
      .map(([matchValue, stepRaw]) => {
        const stepId = typeof stepRaw === "string" ? stepRaw.trim() : "";
        if (!stepId) return null;
        return { matchValue, stepId };
      })
      .filter((entry): entry is SwitchCaseView => entry !== null);
  }
  if (typeof raw === "string" && raw.trim()) {
    try {
      const parsed = JSON.parse(raw);
      return parseSwitchCasesForView(parsed);
    } catch {
      logger.debug("node-detail", "JSON parse failed for switch cases view");
      return [];
    }
  }
  return [];
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
            <span className="text-xs text-muted-foreground">{formatDuration(duration)}</span>
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
              <span className="text-xs text-muted-foreground">
                rule: {safetyDecision.matchedRule}
              </span>
            )}
          </div>
          {safetyDecision.reason && (
            <p className="text-xs text-muted-foreground">{truncate(safetyDecision.reason, 200)}</p>
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
  const [comment, setComment] = useState("");
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionSuccess, setActionSuccess] = useState<string | null>(null);

  const output = runStep?.output ?? {};
  const config = runStep?.config ?? step.config ?? {};
  const approvalStatus = (output.status as string) ?? runStep?.status ?? "pending";
  const approvalActionability =
    (output.actionability as string) ??
    (output.approvalActionability as string) ??
    (approvalStatus === "pending" ? "actionable" : "resolved");
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
    runStep?.status === "waiting" || runStep?.status === "pending";
  const isActionable = approvalActionability === "actionable";

  const isPending = approveJob.isPending || rejectJob.isPending;

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
        { jobId, comment: comment.trim() || undefined },
        {
          onSuccess: () => {
            setActionSuccess("Approved");
            setComment("");
            invalidateRuns();
          },
          onError: (err) => {
            const friendly = friendlyError(err, "approve workflow approval");
            setActionError(`${friendly.title}: ${friendly.description}`);
          },
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
        { jobId, reason, comment: comment.trim() || undefined },
        {
          onSuccess: () => {
            setActionSuccess("Rejected");
            setComment("");
            invalidateRuns();
          },
          onError: (err) => {
            const friendly = friendlyError(err, "reject workflow approval");
            setActionError(`${friendly.title}: ${friendly.description}`);
          },
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
                    : approvalStatus === "invalidated"
                      ? "danger"
                      : approvalStatus === "repaired"
                        ? "info"
                    : "warning"
              }
            >
              {approvalStatus}
            </Badge>
          }
        />
        <InfoRow label="Actionability" value={approvalActionability.replace(/_/g, " ")} />
      </Section>

      {actor && (
        <Section label="Resolution">
          <InfoRow label="Actor" value={truncate(actor, 60)} />
          {resolvedAt && <InfoRow label="Resolved at" value={formatDate(resolvedAt)} />}
          {existingComment && (
            <p className="text-xs text-muted-foreground italic">{truncate(existingComment, 300)}</p>
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
      {isWaiting && isActionable && (
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
              className="bg-[var(--color-success)] hover:bg-[var(--color-success)]/90"
              onClick={handleApprove}
              disabled={isPending}
            >
              {approveJob.isPending ? (
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

      {isWaiting && !isActionable && (
        <Card className="border-border bg-surface2/40">
          <p className="text-xs font-semibold text-ink">Approval no longer actionable</p>
          <p className="mt-1 text-xs text-muted-foreground">
            This workflow step is no longer waiting for a human decision. Refresh the run to inspect the latest lifecycle state.
          </p>
        </Card>
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
          <pre className="rounded-xl bg-surface2/40 px-3 py-2 text-xs font-mono text-ink">
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
    runStep?.status === "running";

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
            <span className="ml-2 text-xs text-muted-foreground">
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
    rs.depends_on?.includes(step.id),
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
          <pre className="rounded-xl bg-surface2/40 px-3 py-2 text-xs font-mono text-ink">
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
                className="flex items-center justify-between rounded-xl border border-border px-3 py-1.5"
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

function SwitchDetail({
  step,
  runStep,
  run,
}: {
  step: WorkflowStep;
  runStep?: WorkflowStep;
  run?: WorkflowRun | null;
}) {
  const config = step.config ?? {};
  const input = (step.input ?? config.input ?? {}) as Record<string, unknown>;
  const expression =
    step.condition ??
    (typeof config.expression === "string" ? config.expression : undefined) ??
    "";
  const switchCases = parseSwitchCasesForView(input.cases ?? config.switchCases ?? config.cases);
  const defaultStep =
    (typeof input.default === "string" && input.default.trim()
      ? input.default.trim()
      : typeof input.default_step === "string" && input.default_step.trim()
        ? input.default_step.trim()
        : typeof config.defaultBranch === "string" && config.defaultBranch.trim()
          ? config.defaultBranch.trim()
          : "");

  const output = runStep?.output ?? {};
  const matchedCase = output.matched_case == null ? "" : String(output.matched_case);
  const targetStep = output.target_step == null ? "" : String(output.target_step);

  const runStepMap = new Map((run?.steps ?? []).map((entry) => [entry.id, entry]));

  return (
    <div className="space-y-4">
      <Section label="Execution">
        <RunStatusBadge status={runStep?.status} />
      </Section>

      {expression && (
        <Section label="Expression">
          <pre className="rounded-xl bg-surface2/40 px-3 py-2 text-xs font-mono text-ink">
            {truncate(expression, 500)}
          </pre>
        </Section>
      )}

      <Section label={`Cases (${switchCases.length})`}>
        {switchCases.length === 0 ? (
          <p className="text-xs text-muted-foreground">No cases configured.</p>
        ) : (
          <div className="space-y-1">
            {switchCases.map((entry, idx) => {
              const stepRun = runStepMap.get(entry.stepId);
              const taken = targetStep !== "" && targetStep === entry.stepId && matchedCase === entry.matchValue;
              return (
                <div
                  key={`${entry.stepId}-${idx}`}
                  className={cn(
                    "rounded-xl border px-3 py-2",
                    taken ? "border-success/40 bg-success/5" : "border-border",
                  )}
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-xs font-mono text-ink">{truncate(entry.matchValue || "(empty)", 120)}</span>
                    <div className="flex items-center gap-2">
                      {taken && <Badge variant="success">taken</Badge>}
                      <RunStatusBadge status={stepRun?.status ?? "pending"} />
                    </div>
                  </div>
                  <p className="mt-1 text-xs text-muted-foreground">{truncate(stepRun?.name || entry.stepId, 120)}</p>
                </div>
              );
            })}
          </div>
        )}
      </Section>

      {defaultStep && (
        <Section label="Default Branch">
          <div className={cn("rounded-xl border px-3 py-2", targetStep === defaultStep ? "border-success/40 bg-success/5" : "border-border")}>
            <div className="flex items-center justify-between gap-2">
              <span className="text-xs text-ink">{truncate(defaultStep, 120)}</span>
              <div className="flex items-center gap-2">
                {targetStep === defaultStep && <Badge variant="success">taken</Badge>}
                <RunStatusBadge status={runStepMap.get(defaultStep)?.status ?? "pending"} />
              </div>
            </div>
          </div>
        </Section>
      )}
    </div>
  );
}

function ParallelDetail({
  step,
  runStep,
  run,
}: {
  step: WorkflowStep;
  runStep?: WorkflowStep;
  run?: WorkflowRun | null;
}) {
  const config = step.config ?? {};
  const input = (step.input ?? config.input ?? {}) as Record<string, unknown>;
  const childStepIDs = Array.isArray(input.steps)
    ? (input.steps as unknown[]).map((entry) => String(entry).trim()).filter(Boolean)
    : Array.isArray(config.parallelSteps)
      ? (config.parallelSteps as unknown[]).map((entry) => String(entry).trim()).filter(Boolean)
      : [];
  const strategy =
    (typeof input.strategy === "string" && input.strategy.trim()
      ? input.strategy
      : typeof config.completionStrategy === "string" && config.completionStrategy.trim()
        ? config.completionStrategy
        : "all") ?? "all";
  const required =
    (typeof input.required === "number" ? input.required : null) ??
    (typeof config.requiredCount === "number" ? config.requiredCount : null);
  const maxParallel =
    (typeof step.max_parallel === "number" ? step.max_parallel : null) ??
    (typeof config.parallelism === "number" ? config.parallelism : null);

  const runStepsByID = new Map((run?.steps ?? []).map((entry) => [entry.id, entry]));
  const childRuns = childStepIDs.map((childID) => runStepsByID.get(childID)).filter(Boolean) as WorkflowStep[];
  const succeeded = childRuns.filter((entry) => entry.status === "succeeded").length;
  const failed = childRuns.filter((entry) => entry.status === "failed" || entry.status === "denied" || entry.status === "timed_out").length;
  const cancelled = childRuns.filter((entry) => entry.status === "cancelled").length;
  const done = childRuns.filter((entry) => ["succeeded", "failed", "denied", "timed_out", "cancelled"].includes(entry.status ?? "")).length;
  const total = childStepIDs.length;
  const progressPct = total > 0 ? Math.round((done / total) * 100) : 0;

  return (
    <div className="space-y-4">
      <Section label="Execution">
        <RunStatusBadge status={runStep?.status} />
      </Section>

      <Section label="Strategy">
        <InfoRow label="Mode" value={strategy} />
        {strategy === "n_of_m" && required != null && (
          <InfoRow label="Required success" value={required} />
        )}
        {maxParallel != null && <InfoRow label="Max parallel" value={maxParallel} />}
      </Section>

      <Section label={`Progress (${done}/${total})`}>
        <div className="h-2 w-full overflow-hidden rounded-full bg-surface2">
          <div className="h-full rounded-full bg-accent transition-all" style={{ width: `${progressPct}%` }} />
        </div>
        <div className="mt-2 grid grid-cols-3 gap-2 text-xs text-muted-foreground">
          <span>succeeded: {succeeded}</span>
          <span>failed: {failed}</span>
          <span>cancelled: {cancelled}</span>
        </div>
      </Section>

      {childStepIDs.length > 0 && (
        <Section label={`Child Steps (${childStepIDs.length})`}>
          <div className="space-y-1">
            {childStepIDs.map((childID) => {
              const child = runStepsByID.get(childID);
              return (
                <div
                  key={childID}
                  className="flex items-center justify-between rounded-xl border border-border px-3 py-1.5"
                >
                  <span className="text-xs text-ink">{child?.name || childID}</span>
                  <RunStatusBadge status={child?.status ?? "pending"} />
                </div>
              );
            })}
          </div>
        </Section>
      )}
    </div>
  );
}

function LoopDetail({
  step,
  runStep,
  run,
}: {
  step: WorkflowStep;
  runStep?: WorkflowStep;
  run?: WorkflowRun | null;
}) {
  const config = step.config ?? {};
  const input = (step.input ?? config.input ?? {}) as Record<string, unknown>;
  const bodyStep =
    (typeof input.body_step === "string" && input.body_step.trim()
      ? input.body_step.trim()
      : typeof input.body === "string" && input.body.trim()
        ? input.body.trim()
        : typeof config.bodyStep === "string" && config.bodyStep.trim()
          ? config.bodyStep.trim()
          : "");
  const maxIterations =
    (typeof input.max_iterations === "number" ? input.max_iterations : null) ??
    (typeof input.maxIterations === "number" ? input.maxIterations : null) ??
    (typeof config.maxIterations === "number" ? config.maxIterations : null);
  const conditionExpr =
    (typeof input.condition === "string" && input.condition.trim()
      ? input.condition.trim()
      : typeof input.while === "string" && input.while.trim()
        ? input.while.trim()
        : typeof config.condition === "string" && config.condition.trim()
          ? config.condition.trim()
          : "");
  const untilExpr =
    (typeof input.until === "string" && input.until.trim()
      ? input.until.trim()
      : typeof config.until === "string" && config.until.trim()
        ? config.until.trim()
        : "");

  const output = runStep?.output ?? {};
  const outputIterations = typeof output.iterations === "number"
    ? Math.max(0, Math.floor(output.iterations))
    : undefined;
  const lastOutput = output.last_output;

  const childRuns = (run?.steps ?? [])
    .map((entry) => ({
      index: parseLoopChildIndex(step.id, entry.id),
      step: entry,
    }))
    .filter((entry): entry is { index: number; step: WorkflowStep } => entry.index != null)
    .sort((a, b) => a.index - b.index);

  const dispatchedIterations = childRuns.length;
  const terminalChildren = childRuns.filter((entry) =>
    ["succeeded", "failed", "denied", "timed_out", "cancelled"].includes(entry.step.status ?? ""),
  ).length;
  const failedChildren = childRuns.filter((entry) =>
    ["failed", "timed_out", "cancelled"].includes(entry.step.status ?? ""),
  ).length;
  const activeChildren = childRuns.filter((entry) =>
    ["pending", "running", "waiting"].includes(entry.step.status ?? ""),
  ).length;

  const completedIterations = outputIterations ?? terminalChildren;
  const iterationBadge = Math.max(dispatchedIterations, completedIterations);
  const progressTotal = maxIterations != null && maxIterations > 0 ? maxIterations : null;
  const progressPct =
    progressTotal != null
      ? Math.max(0, Math.min(100, Math.round((completedIterations / progressTotal) * 100)))
      : null;

  const [openChildren, setOpenChildren] = useState<Record<string, boolean>>({});
  const toggleChild = (childId: string) => {
    setOpenChildren((prev) => ({ ...prev, [childId]: !prev[childId] }));
  };

  return (
    <div className="space-y-4">
      <Section label="Execution">
        <div className="flex items-center justify-between">
          <RunStatusBadge status={runStep?.status} />
          <Badge variant="info">{`iter ${iterationBadge}`}</Badge>
        </div>
        {runStep?.startedAt && <InfoRow label="Started" value={formatDate(runStep.startedAt)} />}
        {runStep?.completedAt && <InfoRow label="Completed" value={formatDate(runStep.completedAt)} />}
      </Section>

      <Section label="Config">
        {bodyStep && <InfoRow label="Body step" value={bodyStep} />}
        {maxIterations != null && <InfoRow label="Max iterations" value={maxIterations} />}
      </Section>

      {conditionExpr && (
        <Section label="Condition (while)">
          <pre className="rounded-xl bg-surface2/40 px-3 py-2 text-xs font-mono text-ink">
            {truncate(conditionExpr, 500)}
          </pre>
        </Section>
      )}

      {untilExpr && (
        <Section label="Until (stop when true)">
          <pre className="rounded-xl bg-surface2/40 px-3 py-2 text-xs font-mono text-ink">
            {truncate(untilExpr, 500)}
          </pre>
        </Section>
      )}

      <Section label={`Progress (${completedIterations}/${progressTotal ?? "\u2014"})`}>
        {progressPct != null && (
          <div className="h-2 w-full overflow-hidden rounded-full bg-surface2">
            <div className="h-full rounded-full bg-accent transition-all" style={{ width: `${progressPct}%` }} />
          </div>
        )}
        <div className="mt-2 grid grid-cols-3 gap-2 text-xs text-muted-foreground">
          <span>dispatched: {dispatchedIterations}</span>
          <span>active: {activeChildren}</span>
          <span>failed: {failedChildren}</span>
        </div>
      </Section>

      {lastOutput !== undefined && (
        <CollapsibleJson label="Last Iteration Output" data={lastOutput} />
      )}

      {childRuns.length > 0 && (
        <Section label={`Iterations (${childRuns.length})`}>
          <div className="space-y-1">
            {childRuns.map((entry) => {
              const child = entry.step;
              const isOpen = !!openChildren[child.id];
              return (
                <div key={child.id} className="rounded-xl border border-border">
                  <button
                    type="button"
                    onClick={() => toggleChild(child.id)}
                    className="flex w-full items-center justify-between gap-2 px-3 py-2 text-left"
                  >
                    <div className="flex items-center gap-2">
                      {isOpen ? (
                        <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
                      ) : (
                        <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
                      )}
                      <span className="text-xs font-medium text-ink">{`Iteration ${entry.index + 1}`}</span>
                    </div>
                    <RunStatusBadge status={child.status ?? "pending"} />
                  </button>
                  {isOpen && (
                    <div className="space-y-2 border-t border-border px-3 py-2">
                      {child.startedAt && <InfoRow label="Started" value={formatDate(child.startedAt)} />}
                      {child.completedAt && <InfoRow label="Completed" value={formatDate(child.completedAt)} />}
                      {child.error && (
                        <Card className="border-danger/40 bg-danger/5">
                          <p className="text-xs font-semibold text-danger">Error</p>
                          <p className="mt-1 text-xs text-ink">{truncate(child.error, 500)}</p>
                        </Card>
                      )}
                      {child.output && (
                        <CollapsibleJson label="Output" data={child.output} />
                      )}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </Section>
      )}
    </div>
  );
}

function SubWorkflowDetail({
  step,
  runStep,
}: {
  step: WorkflowStep;
  runStep?: WorkflowStep;
}) {
  const config = step.config ?? {};
  const input = (step.input ?? config.input ?? {}) as Record<string, unknown>;
  const configuredWorkflowId =
    (typeof input.workflow_id === "string" && input.workflow_id.trim()
      ? input.workflow_id.trim()
      : typeof config.workflowId === "string" && config.workflowId.trim()
        ? config.workflowId.trim()
        : "");
  const inputMapping = input.input_mapping ?? config.inputMapping;
  const outputMapping = input.output_mapping ?? config.outputMapping;
  const outputPath =
    (typeof step.output_path === "string" && step.output_path.trim()
      ? step.output_path.trim()
      : typeof config.outputPath === "string" && config.outputPath.trim()
        ? config.outputPath.trim()
        : "");

  const output = runStep?.output ?? {};
  const childRunId =
    (typeof output.child_run_id === "string" && output.child_run_id.trim()
      ? output.child_run_id.trim()
      : typeof output.run_id === "string" && output.run_id.trim()
        ? output.run_id.trim()
        : "");
  const childWorkflowId =
    (typeof output.child_workflow_id === "string" && output.child_workflow_id.trim()
      ? output.child_workflow_id.trim()
      : typeof output.workflow_id === "string" && output.workflow_id.trim()
        ? output.workflow_id.trim()
        : configuredWorkflowId);
  const childStatus =
    (typeof output.child_status === "string" && output.child_status.trim()
      ? output.child_status
      : typeof output.status === "string" && output.status.trim()
        ? output.status
        : undefined) as WorkflowStep["status"] | undefined;

  let childDuration: number | undefined;
  if (runStep?.startedAt) {
    const end = runStep.completedAt ? new Date(runStep.completedAt).getTime() : Date.now();
    childDuration = end - new Date(runStep.startedAt).getTime();
  }

  return (
    <div className="space-y-4">
      <Section label="Execution">
        <div className="flex items-center justify-between">
          <RunStatusBadge status={runStep?.status} />
          {childDuration != null && (
            <span className="text-xs text-muted-foreground">{formatDuration(childDuration)}</span>
          )}
        </div>
        {childStatus && <InfoRow label="Child status" value={<RunStatusBadge status={childStatus} />} />}
      </Section>

      <Section label="Child Workflow">
        {childWorkflowId ? (
          <Link to={`/workflows/${childWorkflowId}/studio`} className="text-xs font-mono text-accent hover:underline">
            {childWorkflowId}
          </Link>
        ) : (
          <span className="text-xs text-muted-foreground">Not configured</span>
        )}
        {childRunId && childWorkflowId && (
          <div>
            <Link
              to={`/workflows/${childWorkflowId}/runs/${childRunId}`}
              className="text-xs font-mono text-accent hover:underline"
            >
              {childRunId}
            </Link>
          </div>
        )}
      </Section>

      {outputPath && (
        <Section label="Output Path">
          <span className="text-xs font-mono text-ink">{outputPath}</span>
        </Section>
      )}

      <CollapsibleJson label="Input Mapping" data={inputMapping} />
      <CollapsibleJson label="Output Mapping" data={outputMapping} />
      <CollapsibleJson label="Step Output" data={runStep?.output} />
    </div>
  );
}

function TransformDetail({
  step,
  runStep,
}: {
  step: WorkflowStep;
  runStep?: WorkflowStep;
}) {
  const config = step.config ?? {};
  const inputExprs = (step.input ?? config.input ?? {}) as Record<string, unknown>;
  const outputPath =
    (typeof step.output_path === "string" && step.output_path.trim()
      ? step.output_path.trim()
      : typeof config.outputPath === "string" && config.outputPath.trim()
        ? config.outputPath.trim()
        : "");
  const output = runStep?.output ?? {};
  const outputMap = typeof output === "object" && output !== null ? (output as Record<string, unknown>) : {};
  const entries = Object.entries(inputExprs);

  let duration: number | undefined;
  if (runStep?.startedAt && runStep?.completedAt) {
    duration = new Date(runStep.completedAt).getTime() - new Date(runStep.startedAt).getTime();
  }

  return (
    <div className="space-y-4">
      <Section label="Execution">
        <div className="flex items-center justify-between">
          <RunStatusBadge status={runStep?.status} />
          {duration != null && (
            <span className="text-xs text-muted-foreground">{formatDuration(duration)}</span>
          )}
        </div>
        {runStep?.startedAt && <InfoRow label="Started" value={formatDate(runStep.startedAt)} />}
        {runStep?.completedAt && <InfoRow label="Completed" value={formatDate(runStep.completedAt)} />}
      </Section>

      {outputPath && (
        <Section label="Output Path">
          <span className="text-xs font-mono text-ink">{outputPath}</span>
        </Section>
      )}

      <Section label={`Mappings (${entries.length})`}>
        {entries.length === 0 ? (
          <p className="text-xs text-muted-foreground">No input mappings configured.</p>
        ) : (
          <div className="space-y-2">
            {entries.map(([key, expr]) => (
              <div key={key} className="rounded-xl border border-border px-3 py-2">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs font-semibold text-ink">{key}</span>
                  {runStep?.status === "succeeded" && outputMap[key] !== undefined && (
                    <Badge variant="success" className="text-xs">evaluated</Badge>
                  )}
                </div>
                <pre className="mt-1 rounded bg-surface2/40 px-2 py-1 text-xs font-mono text-muted-foreground">
                  {typeof expr === "string" ? expr : JSON.stringify(expr, null, 2)}
                </pre>
                {runStep?.status === "succeeded" && outputMap[key] !== undefined && (
                  <pre className="mt-1 rounded bg-success/5 border border-success/20 px-2 py-1 text-xs font-mono text-ink">
                    {typeof outputMap[key] === "string"
                      ? outputMap[key] as string
                      : JSON.stringify(outputMap[key], null, 2)}
                  </pre>
                )}
              </div>
            ))}
          </div>
        )}
      </Section>

      {runStep?.error && (
        <Card className="border-danger/40 bg-danger/5">
          <p className="text-xs font-semibold text-danger">Error</p>
          <p className="mt-1 text-xs text-ink">{truncate(runStep.error, 500)}</p>
        </Card>
      )}

      <CollapsibleJson label="Full Output" data={runStep?.output} />
    </div>
  );
}

function StorageDetail({
  step,
  runStep,
}: {
  step: WorkflowStep;
  runStep?: WorkflowStep;
}) {
  const config = step.config ?? {};
  const input = (step.input ?? config.input ?? {}) as Record<string, unknown>;
  const operation =
    (typeof input.operation === "string" ? input.operation : undefined) ??
    (typeof config.operation === "string" ? config.operation : undefined) ??
    "";
  const key =
    (typeof input.key === "string" ? input.key : undefined) ??
    (typeof config.key === "string" ? config.key : undefined) ??
    "";
  const outputPath =
    (typeof step.output_path === "string" && step.output_path.trim()
      ? step.output_path.trim()
      : typeof config.outputPath === "string" && config.outputPath.trim()
        ? config.outputPath.trim()
        : "");
  const output = runStep?.output ?? {};
  const outputMap = typeof output === "object" && output !== null ? (output as Record<string, unknown>) : {};

  let duration: number | undefined;
  if (runStep?.startedAt && runStep?.completedAt) {
    duration = new Date(runStep.completedAt).getTime() - new Date(runStep.startedAt).getTime();
  }

  const opVariant: Record<string, "success" | "danger" | "warning" | "info"> = {
    read: "info",
    write: "success",
    delete: "danger",
  };

  return (
    <div className="space-y-4">
      <Section label="Execution">
        <div className="flex items-center justify-between">
          <RunStatusBadge status={runStep?.status} />
          {duration != null && (
            <span className="text-xs text-muted-foreground">{formatDuration(duration)}</span>
          )}
        </div>
        {runStep?.startedAt && <InfoRow label="Started" value={formatDate(runStep.startedAt)} />}
        {runStep?.completedAt && <InfoRow label="Completed" value={formatDate(runStep.completedAt)} />}
      </Section>

      <Section label="Operation">
        <div className="flex items-center gap-2">
          {operation && (
            <Badge variant={opVariant[operation] ?? "default"}>{operation}</Badge>
          )}
          <Badge variant="info">context</Badge>
        </div>
      </Section>

      {key && (
        <Section label="Key Path">
          <pre className="rounded-xl bg-surface2/40 px-3 py-2 text-xs font-mono text-ink">
            {truncate(key, 200)}
          </pre>
        </Section>
      )}

      {outputPath && (
        <Section label="Output Path">
          <span className="text-xs font-mono text-ink">{outputPath}</span>
        </Section>
      )}

      {runStep?.status === "succeeded" && outputMap.value !== undefined && (
        <Section label="Value">
          <pre className="rounded-xl border border-success/20 bg-success/5 px-3 py-2 text-xs font-mono text-ink">
            {typeof outputMap.value === "string"
              ? outputMap.value
              : JSON.stringify(outputMap.value, null, 2)}
          </pre>
        </Section>
      )}

      {runStep?.error && (
        <Card className="border-danger/40 bg-danger/5">
          <p className="text-xs font-semibold text-danger">Error</p>
          <p className="mt-1 text-xs text-ink">{truncate(runStep.error, 500)}</p>
        </Card>
      )}

      <CollapsibleJson label="Full Output" data={runStep?.output} />
    </div>
  );
}

function HttpDetail({
  step,
  runStep,
}: {
  step: WorkflowStep;
  runStep?: WorkflowStep;
}) {
  const config = step.config ?? {};
  const input = (step.input ?? config.input ?? {}) as Record<string, unknown>;
  const method =
    (typeof input.method === "string" ? input.method.toUpperCase() : undefined) ??
    (typeof config.method === "string" ? config.method.toUpperCase() : undefined) ??
    "";
  const url =
    (typeof input.url === "string" ? input.url : undefined) ??
    (typeof config.url === "string" ? config.url : undefined) ??
    "";
  const headers = (input.headers ?? config.headers) as Record<string, unknown> | undefined;

  let duration: number | undefined;
  if (runStep?.startedAt && runStep?.completedAt) {
    duration = new Date(runStep.completedAt).getTime() - new Date(runStep.startedAt).getTime();
  }

  const methodVariant: Record<string, "success" | "danger" | "warning" | "info"> = {
    GET: "info",
    POST: "success",
    PUT: "warning",
    PATCH: "warning",
    DELETE: "danger",
  };

  return (
    <div className="space-y-4">
      <Section label="Execution">
        <div className="flex items-center justify-between">
          <RunStatusBadge status={runStep?.status} />
          {duration != null && (
            <span className="text-xs text-muted-foreground">{formatDuration(duration)}</span>
          )}
        </div>
        {runStep?.startedAt && <InfoRow label="Started" value={formatDate(runStep.startedAt)} />}
        {runStep?.completedAt && <InfoRow label="Completed" value={formatDate(runStep.completedAt)} />}
      </Section>

      <Section label="Request">
        <div className="flex items-center gap-2">
          {method && <Badge variant={methodVariant[method] ?? "default"}>{method}</Badge>}
        </div>
        {url && (
          <pre className="mt-1.5 rounded-xl bg-surface2/40 px-3 py-2 text-xs font-mono text-ink break-all whitespace-pre-wrap">
            {truncate(url, 300)}
          </pre>
        )}
      </Section>

      {headers && Object.keys(headers).length > 0 && (
        <CollapsibleJson label="Headers" data={headers} />
      )}

      {runStep?.error && (
        <Card className="border-danger/40 bg-danger/5">
          <p className="text-xs font-semibold text-danger">Error</p>
          <p className="mt-1 text-xs text-ink">{truncate(runStep.error, 500)}</p>
        </Card>
      )}

      <CollapsibleJson label="Request Body" data={input.body ?? config.body} />
      <CollapsibleJson label="Response" data={runStep?.output} />
    </div>
  );
}

function ErrorTriggerDetail({
  step,
  runStep,
}: {
  step: WorkflowStep;
  runStep?: WorkflowStep;
}) {
  const config = step.config ?? {};
  const input = (step.input ?? config.input ?? {}) as Record<string, unknown>;
  const catchFrom =
    (typeof input.catch_from === "string" ? input.catch_from : undefined) ??
    (typeof config.catchFrom === "string" ? config.catchFrom : undefined) ??
    "";
  const retryCount =
    typeof input.retry_count === "number" ? input.retry_count
    : typeof config.retryCount === "number" ? config.retryCount
    : undefined;
  const retryDelay =
    (typeof input.retry_delay === "string" ? input.retry_delay : undefined) ??
    (typeof config.retryDelay === "string" ? config.retryDelay : undefined) ??
    "";
  const onError = step.on_error ?? (typeof config.onError === "string" ? config.onError : "");

  let duration: number | undefined;
  if (runStep?.startedAt && runStep?.completedAt) {
    duration = new Date(runStep.completedAt).getTime() - new Date(runStep.startedAt).getTime();
  }

  return (
    <div className="space-y-4">
      <Section label="Execution">
        <div className="flex items-center justify-between">
          <RunStatusBadge status={runStep?.status} />
          {duration != null && (
            <span className="text-xs text-muted-foreground">{formatDuration(duration)}</span>
          )}
        </div>
        {runStep?.startedAt && <InfoRow label="Started" value={formatDate(runStep.startedAt)} />}
        {runStep?.completedAt && <InfoRow label="Completed" value={formatDate(runStep.completedAt)} />}
      </Section>

      <Section label="Error Handling">
        {catchFrom && <InfoRow label="Catches from" value={catchFrom} />}
        {onError && <InfoRow label="On error action" value={onError} />}
        {retryCount != null && <InfoRow label="Retry count" value={String(retryCount)} />}
        {retryDelay && <InfoRow label="Retry delay" value={retryDelay} />}
      </Section>

      {runStep?.error && (
        <Card className="border-danger/40 bg-danger/5">
          <p className="text-xs font-semibold text-danger">Caught Error</p>
          <p className="mt-1 text-xs text-ink">{truncate(runStep.error, 500)}</p>
        </Card>
      )}

      <CollapsibleJson label="Output" data={runStep?.output} />
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
  inline,
}: NodeDetailPanelProps) {
  // Find matching run step
  const runStep = step && run?.steps
    ? run.steps.find((rs) => rs.id === step.id)
    : undefined;

  const stepIcon = step ? (STEP_ICONS[step.type] ?? STEP_ICONS.job) : null;

  const content = step ? (
    <div className={cn("space-y-6", inline && "p-4")}>
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3 min-w-0">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-surface2 text-muted-foreground">
            {stepIcon}
          </div>
          <div className="min-w-0">
            <h3
              className="truncate font-display text-base font-semibold text-ink"
              title={step.name || step.id}
            >
              {truncate(step.name || step.id, 60)}
            </h3>
            <Badge variant="info" className="text-xs">
              {step.type}
            </Badge>
          </div>
        </div>
        <button
          type="button"
          onClick={onClose}
          className="rounded-full p-1.5 hover:bg-surface2 transition"
        >
          <X className="h-4 w-4 text-muted-foreground" />
        </button>
      </div>

      {/* Step-type-specific content */}
      {["job", "worker", "agent-task", "pack-action", "tool-call"].includes(step.type) && (
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
      {step.type === "switch" && (
        <SwitchDetail step={step} runStep={runStep} run={run} />
      )}
      {step.type === "delay" && (
        <DelayDetail step={step} runStep={runStep} />
      )}
      {(step.type === "fan-out" || step.type === "fanout") && (
        <FanOutDetail step={step} runStep={runStep} run={run} />
      )}
      {step.type === "parallel" && (
        <ParallelDetail step={step} runStep={runStep} run={run} />
      )}
      {step.type === "loop" && (
        <LoopDetail step={step} runStep={runStep} run={run} />
      )}
      {(step.type === "sub-workflow" || step.type === "subworkflow") && (
        <SubWorkflowDetail step={step} runStep={runStep} />
      )}
      {step.type === "transform" && (
        <TransformDetail step={step} runStep={runStep} />
      )}
      {step.type === "storage" && (
        <StorageDetail step={step} runStep={runStep} />
      )}
      {step.type === "http" && (
        <HttpDetail step={step} runStep={runStep} />
      )}
      {step.type === "error-trigger" && (
        <ErrorTriggerDetail step={step} runStep={runStep} />
      )}
      {step.type === "notify" && (
        <GenericDetail runStep={runStep} />
      )}
      {!["job", "worker", "agent-task", "pack-action", "tool-call", "approval", "condition", "switch", "delay", "fan-out", "fanout", "parallel", "loop", "sub-workflow", "subworkflow", "transform", "storage", "http", "error-trigger", "notify"].includes(step.type) && (
        <GenericDetail runStep={runStep} />
      )}
    </div>
  ) : null;

  if (inline) {
    return content;
  }

  return (
    <Drawer open={step !== null} onClose={onClose} size="sm">
      {content}
    </Drawer>
  );
}

