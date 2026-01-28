import { useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { epochToMillis, formatDateTime, formatDuration } from "../lib/format";
import { useEventStore } from "../state/events";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { ApprovalStatusBadge, RunStatusBadge } from "../components/StatusBadge";
import { Drawer } from "../components/ui/Drawer";
import { Input } from "../components/ui/Input";
import { WorkflowCanvas } from "../components/workflow/WorkflowCanvas";
import { ActivityStream } from "../components/activity/ActivityStream";
import { useRunChat } from "../hooks/useRunChat";
import type { ActivityItem, SafetyDecision } from "../types/activity";
import type { ApprovalItem, JobDetail } from "../types/api";

const detailTabs = ["Overview", "Timeline", "DAG", "Input/Output", "Jobs", "Logs", "Audit Log"] as const;

export function RunDetailPage() {
  const { runId } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const runIdValue = runId?.trim() || "";
  const runIdValid = runIdValue.length >= 20;
  const [activeTab, setActiveTab] = useState<(typeof detailTabs)[number]>("Overview");
  const [selectedJobId, setSelectedJobId] = useState<string | null>(null);
  const [approvalReason, setApprovalReason] = useState("");
  const [approvalNote, setApprovalNote] = useState("");

  // Chat hook for real-time agent conversation
  const chat = useRunChat(runIdValid ? runIdValue : undefined);

  const runQuery = useQuery({
    queryKey: ["run", runIdValue],
    queryFn: () => api.getRun(runIdValue),
    enabled: runIdValid,
  });

  const workflowQuery = useQuery({
    queryKey: ["workflow", runQuery.data?.workflow_id],
    queryFn: () => api.getWorkflow(runQuery.data?.workflow_id as string),
    enabled: Boolean(runQuery.data?.workflow_id),
  });

  const timelineQuery = useQuery({
    queryKey: ["timeline", runIdValue],
    queryFn: () => api.getRunTimeline(runIdValue),
    enabled: runIdValid,
  });

  const approvalsQuery = useQuery({
    queryKey: ["approvals", "run", runId],
    queryFn: () => api.listApprovals(200),
    enabled: runIdValid,
  });

  const failureStep = useMemo(() => {
    const steps = Object.values(runQuery.data?.steps || {});
    const failures = steps.filter((step) => ["failed", "timed_out", "cancelled"].includes(step.status));
    if (failures.length === 0) {
      return undefined;
    }
    return failures.sort((a, b) => (b.completed_at || "").localeCompare(a.completed_at || ""))[0];
  }, [runQuery.data]);
  const failureJobId = failureStep?.job_id;
  const failureJobQuery = useQuery({
    queryKey: ["job", "failure", failureJobId],
    queryFn: () => api.getJob(failureJobId as string),
    enabled: Boolean(failureJobId),
  });

  const jobQuery = useQuery({
    queryKey: ["job", selectedJobId],
    queryFn: () => api.getJob(selectedJobId as string),
    enabled: Boolean(selectedJobId),
  });

  const latestEvent = useEventStore((state) => state.events[0]);

  const approvalsByJob = useMemo(() => {
    const map = new Map<string, ApprovalItem>();
    approvalsQuery.data?.items.forEach((item) => {
      map.set(item.job.id, item);
    });
    return map;
  }, [approvalsQuery.data]);

  useEffect(() => {
    if (!latestEvent || !runId) {
      return;
    }
    const runMatch = latestEvent.runId === runId;
    const jobMatch = latestEvent.jobId
      ? Object.values(runQuery.data?.steps || {}).some((step) => step.job_id === latestEvent.jobId)
      : false;
    if (runMatch || jobMatch) {
      queryClient.invalidateQueries({ queryKey: ["run", runId] });
      queryClient.invalidateQueries({ queryKey: ["timeline", runId] });
    }
  }, [latestEvent, runId, runQuery.data, queryClient]);

  const rerunMutation = useMutation({
    mutationFn: (payload: { runId: string; fromStep?: string }) =>
      api.rerunRun(payload.runId, { fromStep: payload.fromStep }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["run", runId] });
      queryClient.invalidateQueries({ queryKey: ["runs"] });
      queryClient.invalidateQueries({ queryKey: ["timeline", runId] });
    },
  });

  const cancelMutation = useMutation({
    mutationFn: ({ workflowId, id }: { workflowId: string; id: string }) => api.cancelRun(workflowId, id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["run", runId] }),
  });

  const approveJobMutation = useMutation({
    mutationFn: (jobId: string) => api.approveJob(jobId, { reason: approvalReason, note: approvalNote }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["approvals"] });
      queryClient.invalidateQueries({ queryKey: ["run", runId] });
      queryClient.invalidateQueries({ queryKey: ["timeline", runId] });
    },
  });

  const rejectJobMutation = useMutation({
    mutationFn: (jobId: string) => api.rejectJob(jobId, { reason: approvalReason, note: approvalNote }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["approvals"] });
      queryClient.invalidateQueries({ queryKey: ["run", runId] });
      queryClient.invalidateQueries({ queryKey: ["timeline", runId] });
    },
  });

  const run = runQuery.data;
  const runSteps = Object.values(run?.steps || {});
  const runApprovals = runSteps
    .map((step) => (step.job_id ? approvalsByJob.get(step.job_id) : undefined))
    .filter((item): item is ApprovalItem => Boolean(item));

  const activityItems = useMemo<ActivityItem[]>(() => {
    if (!run) {
      return [];
    }
    const items: ActivityItem[] = [];
    const toMillis = (value?: string | number) => {
      if (!value) return 0;
      const date = new Date(value);
      const time = date.getTime();
      return Number.isNaN(time) ? 0 : time;
    };

    chat.messages.forEach((message) => {
      items.push({
        id: `msg-${message.id}`,
        type: "message",
        role: message.role === "agent" ? "agent" : message.role === "system" ? "system" : "user",
        timestamp: message.created_at,
        content: message.content,
        metadata: {
          step_id: message.step_id,
          job_id: message.job_id,
        },
      });
    });

    (timelineQuery.data || []).forEach((event) => {
      items.push({
        id: `timeline-${event.time}-${event.type}-${event.job_id || event.step_id || ""}`,
        type: "state_change",
        role: "system",
        timestamp: event.time,
        content: event.message || event.type,
        payload: {
          from_step: event.step_id,
          to_step: event.status,
        },
        metadata: {
          step_id: event.step_id,
          job_id: event.job_id,
        },
      });
    });

    runApprovals.forEach((approval) => {
      const decisionRaw = approval.decision ? approval.decision.toUpperCase() : "";
      const decision =
        decisionRaw === "APPROVED" || decisionRaw === "ALLOW"
          ? "ALLOW"
          : decisionRaw === "DENIED" || decisionRaw === "DENY"
          ? "DENY"
          : approval.approval_required
          ? "REQUIRE_APPROVAL"
          : "PENDING";
      const approvalTime = epochToMillis(approval.job.updated_at);
      const timestamp =
        (approvalTime ? new Date(approvalTime).toISOString() : null) ||
        run.started_at ||
        run.created_at ||
        new Date().toISOString();
      items.push({
        id: `approval-${approval.job.id}`,
        type: "safety_event",
        role: "governance",
        timestamp,
        content:
          approval.policy_reason ||
          approval.job.safety_reason ||
          approval.job.topic ||
          "Approval required for this job.",
        payload: {
          policy_name: approval.policy_rule_id || approval.policy_snapshot || approval.job.topic || "Policy review",
          decision: decision as SafetyDecision,
          requires_action: approval.approval_required ?? decision === "REQUIRE_APPROVAL",
        },
        metadata: {
          job_id: approval.job.id,
          policy_snapshot: approval.policy_snapshot,
        },
      });
    });

    return items.sort((a, b) => toMillis(a.timestamp) - toMillis(b.timestamp));
  }, [chat.messages, timelineQuery.data, runApprovals, run?.created_at, run?.started_at, run]);

  if (runIdValue && !runIdValid) {
    return <div className="text-sm text-muted">Run ID looks truncated. Use the full ID from the Runs list.</div>;
  }
  if (runQuery.isLoading) {
    return <div className="text-sm text-muted">Loading run details...</div>;
  }
  if (runQuery.isError || !run) {
    return <div className="text-sm text-muted">Run not found.</div>;
  }

  const primaryApproval = runApprovals[0];
  const failureJob = failureJobQuery.data;
  const failureReason =
    failureJob?.error_message ||
    failureJob?.error_status ||
    failureJob?.error_code ||
    (run.error && typeof run.error === "string"
      ? run.error
      : (run.error as Record<string, unknown> | undefined)?.message
      ? String((run.error as Record<string, unknown>).message)
      : run.error
      ? JSON.stringify(run.error)
      : "");

  const completedSteps = runSteps.filter((step) =>
    ["succeeded", "failed", "cancelled", "timed_out"].includes(step.status)
  ).length;
  const activeStep = runSteps.find((step) => ["running", "waiting", "pending"].includes(step.status));
  const retryCount = runSteps.reduce((sum, step) => sum + Math.max(0, (step.attempts || 1) - 1), 0);
  const contextPayload = run.context && Object.keys(run.context).length > 0 ? run.context : run.input;
  const totalCost = typeof run.total_cost === "number" ? `$${run.total_cost.toFixed(4)}` : "-";

  const stepStatusClass = (status: string) => {
    if (["running", "waiting"].includes(status)) return "bg-accent animate-pulse";
    if (["succeeded", "completed"].includes(status)) return "bg-success";
    if (["failed", "timed_out", "cancelled", "denied"].includes(status)) return "bg-danger";
    return "bg-muted";
  };

  const policyLinkForApproval = (approval?: ApprovalItem) => {
    if (!approval) {
      return "";
    }
    const params = new URLSearchParams();
    if (approval.job.id) {
      params.set("job_id", approval.job.id);
    }
    if (approval.job.topic) {
      params.set("topic", approval.job.topic);
    }
    if (approval.job.tenant) {
      params.set("tenant", approval.job.tenant);
    }
    if (approval.job.capability) {
      params.set("capability", approval.job.capability);
    }
    if (approval.job.pack_id) {
      params.set("pack_id", approval.job.pack_id);
    }
    if (approval.job.actor_id) {
      params.set("actor_id", approval.job.actor_id);
    }
    if (approval.job.actor_type) {
      params.set("actor_type", approval.job.actor_type);
    }
    if (approval.job.risk_tags?.length) {
      params.set("risk_tags", approval.job.risk_tags.join(","));
    }
    if (approval.job.requires?.length) {
      params.set("requires", approval.job.requires.join(","));
    }
    return `/policy?${params.toString()}`;
  };

  const policyLinkForJob = (job?: JobDetail) => {
    if (!job) {
      return "";
    }
    const params = new URLSearchParams();
    params.set("job_id", job.id);
    if (job.topic) {
      params.set("topic", job.topic);
    }
    if (job.tenant) {
      params.set("tenant", job.tenant);
    }
    if (job.capability) {
      params.set("capability", job.capability);
    }
    if (job.pack_id) {
      params.set("pack_id", job.pack_id);
    }
    if (job.actor_id) {
      params.set("actor_id", job.actor_id);
    }
    if (job.actor_type) {
      params.set("actor_type", job.actor_type);
    }
    if (job.risk_tags?.length) {
      params.set("risk_tags", job.risk_tags.join(","));
    }
    if (job.requires?.length) {
      params.set("requires", job.requires.join(","));
    }
    return `/policy?${params.toString()}`;
  };

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Run Summary</CardTitle>
          <RunStatusBadge status={run.status} />
        </CardHeader>
        <div className="grid gap-4 lg:grid-cols-4">
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Outcome</div>
            <div className="text-sm font-semibold text-ink">{run.status}</div>
          </div>
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Root step</div>
            <div className="text-sm font-semibold text-ink">
              {failureStep?.step_id || (runApprovals.length ? "Waiting on approvals" : "-")}
            </div>
          </div>
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Last error</div>
            <div className="text-sm font-semibold text-ink">{failureReason || "-"}</div>
          </div>
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Focus</div>
            <div className="text-sm font-semibold text-ink">{failureJobId || primaryApproval?.job.id || "-"}</div>
          </div>
        </div>
        <div className="mt-4 flex flex-wrap gap-2">
          {failureJobId ? (
            <Button variant="primary" size="sm" type="button" onClick={() => navigate(`/jobs/${failureJobId}`)}>
              Open failed job
            </Button>
          ) : null}
          {failureJob ? (
            <Button variant="outline" size="sm" type="button" onClick={() => navigate(policyLinkForJob(failureJob))}>
              Open policy
            </Button>
          ) : null}
          {primaryApproval ? (
            <Button variant="outline" size="sm" type="button" onClick={() => navigate(policyLinkForApproval(primaryApproval))}>
              Open approval
            </Button>
          ) : null}
          {["failed", "timed_out"].includes(run.status) ? (
            <Button
              variant="subtle"
              size="sm"
              type="button"
              onClick={() => rerunMutation.mutate({ runId: run.id })}
              disabled={rerunMutation.isPending}
            >
              Rerun
            </Button>
          ) : null}
          <Button
            variant="subtle"
            size="sm"
            type="button"
            onClick={() => setActiveTab("Timeline")}
          >
            View timeline
          </Button>
        </div>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Run {run.id.slice(0, 12)}</CardTitle>
          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              size="sm"
              type="button"
              onClick={() => rerunMutation.mutate({ runId: run.id })}
              disabled={rerunMutation.isPending}
            >
              Rerun
            </Button>
            <Button
              variant="subtle"
              size="sm"
              type="button"
              onClick={() => navigate(`/workflows/${run.workflow_id}`)}
            >
              Open workflow
            </Button>
            <Button
              variant="danger"
              size="sm"
              type="button"
              onClick={() => cancelMutation.mutate({ workflowId: run.workflow_id, id: run.id })}
              disabled={cancelMutation.isPending}
            >
              Cancel
            </Button>
          </div>
        </CardHeader>
        <div className="grid gap-4 lg:grid-cols-4">
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Status</div>
            <RunStatusBadge status={run.status} />
          </div>
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Duration</div>
            <div className="text-sm font-semibold text-ink">{formatDuration(run.started_at || run.created_at, run.completed_at)}</div>
          </div>
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Workflow</div>
            <div className="text-sm font-semibold text-ink">{workflowQuery.data?.name || run.workflow_id}</div>
          </div>
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Started</div>
            <div className="text-sm font-semibold text-ink">{formatDateTime(run.started_at || run.created_at)}</div>
          </div>
        </div>
      </Card>

      <div className="grid gap-6 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        <ActivityStream
          items={activityItems}
          isLoading={chat.isLoading || timelineQuery.isLoading}
          runStatus={run.status}
          isSending={chat.isSending}
          onSendMessage={chat.sendMessage}
          onApprove={(jobId) => approveJobMutation.mutate(jobId)}
          onReject={(jobId) => rejectJobMutation.mutate(jobId)}
        />
        <div className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle>Active Context</CardTitle>
            </CardHeader>
            <div className="rounded-2xl border border-border bg-white/70 p-3 text-xs text-muted">
              <pre>{JSON.stringify(contextPayload || {}, null, 2)}</pre>
            </div>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Safety Status</CardTitle>
              <RunStatusBadge status={run.status} />
            </CardHeader>
            <div className="space-y-3">
              {runApprovals.length ? (
                <>
                  <div className="rounded-2xl border border-border bg-white/70 p-3">
                    <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Approval notes</div>
                    <div className="mt-2 grid gap-2 lg:grid-cols-2">
                      <Input
                        value={approvalReason}
                        onChange={(event) => setApprovalReason(event.target.value)}
                        placeholder="Reason"
                      />
                      <Input
                        value={approvalNote}
                        onChange={(event) => setApprovalNote(event.target.value)}
                        placeholder="Note"
                      />
                    </div>
                  </div>
                  {runApprovals.map((approval) => (
                    <div key={approval.job.id} className="rounded-2xl border border-border bg-white/70 p-3">
                      <div className="flex items-center justify-between gap-2">
                        <div>
                          <div className="text-sm font-semibold text-ink">{approval.job.topic || "Job approval"}</div>
                          <div className="text-xs text-muted">Job {approval.job.id.slice(0, 8)}</div>
                        </div>
                        <ApprovalStatusBadge required={approval.approval_required} />
                      </div>
                      <div className="mt-3 flex flex-wrap gap-2">
                        <Button
                          variant="outline"
                          size="sm"
                          type="button"
                          onClick={() => navigate(policyLinkForApproval(approval))}
                        >
                          Policy
                        </Button>
                        <Button
                          variant="primary"
                          size="sm"
                          type="button"
                          onClick={() => approveJobMutation.mutate(approval.job.id)}
                          disabled={approveJobMutation.isPending}
                        >
                          Approve
                        </Button>
                        <Button
                          variant="danger"
                          size="sm"
                          type="button"
                          onClick={() => rejectJobMutation.mutate(approval.job.id)}
                          disabled={rejectJobMutation.isPending}
                        >
                          Reject
                        </Button>
                      </div>
                    </div>
                  ))}
                </>
              ) : (
                <div className="rounded-2xl border border-dashed border-border p-4 text-sm text-muted">
                  No approvals waiting on this run.
                </div>
              )}
            </div>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Execution Mini-Map</CardTitle>
              <div className="text-xs text-muted">{completedSteps}/{runSteps.length} completed</div>
            </CardHeader>
            {runSteps.length === 0 ? (
              <div className="rounded-2xl border border-dashed border-border p-4 text-sm text-muted">
                No steps reported yet.
              </div>
            ) : (
              <div className="flex flex-wrap gap-2">
                {runSteps.map((step) => (
                  <div key={step.step_id} className="flex items-center gap-2 rounded-full border border-border bg-white/70 px-3 py-1">
                    <span className={`h-2 w-2 rounded-full ${stepStatusClass(step.status)}`} />
                    <span className="text-[10px] font-semibold text-muted">{step.step_id}</span>
                  </div>
                ))}
              </div>
            )}
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Resource Metrics</CardTitle>
            </CardHeader>
            <div className="grid gap-3 lg:grid-cols-2">
              <div className="rounded-2xl border border-border bg-white/70 p-3">
                <div className="text-xs uppercase tracking-[0.2em] text-muted">Duration</div>
                <div className="text-sm font-semibold text-ink">
                  {formatDuration(run.started_at || run.created_at, run.completed_at)}
                </div>
              </div>
              <div className="rounded-2xl border border-border bg-white/70 p-3">
                <div className="text-xs uppercase tracking-[0.2em] text-muted">Active step</div>
                <div className="text-sm font-semibold text-ink">{activeStep?.step_id || "-"}</div>
              </div>
              <div className="rounded-2xl border border-border bg-white/70 p-3">
                <div className="text-xs uppercase tracking-[0.2em] text-muted">Steps</div>
                <div className="text-sm font-semibold text-ink">{runSteps.length}</div>
              </div>
              <div className="rounded-2xl border border-border bg-white/70 p-3">
                <div className="text-xs uppercase tracking-[0.2em] text-muted">Retries</div>
                <div className="text-sm font-semibold text-ink">{retryCount}</div>
              </div>
              <div className="rounded-2xl border border-border bg-white/70 p-3 lg:col-span-2">
                <div className="text-xs uppercase tracking-[0.2em] text-muted">Estimated cost</div>
                <div className="text-sm font-semibold text-ink">{totalCost}</div>
              </div>
            </div>
          </Card>
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Run Details</CardTitle>
          <Button variant="ghost" size="sm" type="button" onClick={() => setActiveTab("Timeline")}>
            Jump to timeline
          </Button>
        </CardHeader>
        <div className="flex flex-wrap gap-2 border-b border-border pb-4">
          {detailTabs.map((tab) => (
            <button
              key={tab}
              type="button"
              onClick={() => setActiveTab(tab)}
              className={
                tab === activeTab
                  ? "rounded-full bg-[color:rgba(15,127,122,0.16)] px-4 py-2 text-xs font-semibold uppercase tracking-[0.2em] text-accent"
                  : "rounded-full border border-border px-4 py-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted"
              }
            >
              {tab}
            </button>
          ))}
        </div>
        <div className="pt-6">
          {activeTab === "Overview" && (
            <div className="space-y-4">
              {Object.values(run.steps || {}).length === 0 ? (
                <div className="text-sm text-muted">No steps reported yet.</div>
              ) : (
                Object.values(run.steps || {}).map((step) => {
                  const approval = step.job_id ? approvalsByJob.get(step.job_id) : undefined;
                  return (
                    <div key={step.step_id} className="rounded-2xl border border-border bg-white/70 p-4">
                      <div className="flex items-center justify-between">
                        <div className="text-sm font-semibold text-ink">{step.step_id}</div>
                        <span className="text-xs text-muted">{step.status}</span>
                      </div>
                      <div className="mt-2 text-xs text-muted">
                        {step.job_id ? `Job ${step.job_id}` : "No job yet"}
                      </div>
                      {step.job_id && approval ? (
                        <div className="mt-3 flex flex-wrap items-center gap-2">
                          <ApprovalStatusBadge required={approval.approval_required} />
                          <Button
                            variant="outline"
                            size="sm"
                            type="button"
                            onClick={() => navigate(policyLinkForApproval(approval))}
                          >
                            Policy
                          </Button>
                          <Button
                            variant="primary"
                            size="sm"
                            type="button"
                            onClick={() => approveJobMutation.mutate(step.job_id!)}
                            disabled={approveJobMutation.isPending}
                          >
                            Approve
                          </Button>
                          <Button
                            variant="outline"
                            size="sm"
                            type="button"
                            onClick={() => rerunMutation.mutate({ runId: run.id, fromStep: step.step_id })}
                            disabled={rerunMutation.isPending}
                          >
                            Replay from step
                          </Button>
                          <Button
                            variant="danger"
                            size="sm"
                            type="button"
                            onClick={() => rejectJobMutation.mutate(step.job_id!)}
                            disabled={rejectJobMutation.isPending}
                          >
                            Reject
                          </Button>
                        </div>
                      ) : null}
                    </div>
                  );
                })
              )}
            </div>
          )}

          {activeTab === "Timeline" && (
            <div className="space-y-3">
              {timelineQuery.data?.length ? (
                timelineQuery.data.map((event) => (
                  <div key={`${event.time}-${event.type}`} className="rounded-2xl border border-border bg-white/70 p-3">
                    <div className="text-xs text-muted">{formatDateTime(event.time)}</div>
                    <div className="text-sm font-semibold text-ink">{event.type}</div>
                    <div className="text-xs text-muted">
                      {event.step_id ? `Step ${event.step_id}` : ""}
                      {event.job_id ? ` · Job ${event.job_id}` : ""}
                      {event.status ? ` · ${event.status}` : ""}
                    </div>
                    {event.message ? <div className="text-xs text-muted">{event.message}</div> : null}
                  </div>
                ))
              ) : (
                <div className="text-sm text-muted">No timeline events recorded yet.</div>
              )}
            </div>
          )}

          {activeTab === "DAG" && (
            <WorkflowCanvas workflow={workflowQuery.data} run={runQuery.data} height={460} />
          )}

          {activeTab === "Input/Output" && (
            <div className="grid gap-4 lg:grid-cols-2">
              <div className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="mb-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted">Input</div>
                <pre className="text-xs text-ink">{JSON.stringify(run.input || {}, null, 2)}</pre>
              </div>
              <div className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="mb-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted">Output</div>
                <pre className="text-xs text-ink">{JSON.stringify(run.output || run.error || {}, null, 2)}</pre>
              </div>
            </div>
          )}

          {activeTab === "Jobs" && (
            <div className="space-y-3">
              {Object.values(run.steps || {}).length === 0 ? (
                <div className="text-sm text-muted">No jobs created for this run.</div>
              ) : (
                Object.values(run.steps || {}).map((step) => {
                  const approval = step.job_id ? approvalsByJob.get(step.job_id) : undefined;
                  return (
                    <div key={step.step_id} className="rounded-2xl border border-border bg-white/70 p-4">
                      <div className="flex flex-col gap-2 lg:flex-row lg:items-center lg:justify-between">
                        <div>
                          <div className="text-sm font-semibold text-ink">{step.step_id}</div>
                          <div className="text-xs text-muted">{step.job_id || "No job"}</div>
                        </div>
                        <div className="flex flex-wrap gap-2">
                          {approval ? <ApprovalStatusBadge required={approval.approval_required} /> : null}
                          <Button
                            variant="outline"
                            size="sm"
                            type="button"
                            onClick={() => step.job_id && setSelectedJobId(step.job_id)}
                            disabled={!step.job_id}
                          >
                            View job
                          </Button>
                          <Button
                            variant="outline"
                            size="sm"
                            type="button"
                            onClick={() => rerunMutation.mutate({ runId: run.id, fromStep: step.step_id })}
                            disabled={rerunMutation.isPending}
                          >
                            Replay from step
                          </Button>
                        </div>
                      </div>
                      {step.job_id && approval ? (
                        <div className="mt-3 flex flex-wrap gap-2">
                          <Button
                            variant="outline"
                            size="sm"
                            type="button"
                            onClick={() => navigate(policyLinkForApproval(approval))}
                          >
                            Policy
                          </Button>
                          <Button
                            variant="primary"
                            size="sm"
                            type="button"
                            onClick={() => approveJobMutation.mutate(step.job_id!)}
                            disabled={approveJobMutation.isPending}
                          >
                            Approve
                          </Button>
                          <Button
                            variant="danger"
                            size="sm"
                            type="button"
                            onClick={() => rejectJobMutation.mutate(step.job_id!)}
                            disabled={rejectJobMutation.isPending}
                          >
                            Reject
                          </Button>
                        </div>
                      ) : null}
                    </div>
                  );
                })
              )}
            </div>
          )}

          {activeTab === "Logs" && (
            <div className="rounded-2xl border border-border bg-black p-4 font-mono text-xs text-green-400 h-96 overflow-y-auto">
              <div>[INFO] Run {run.id} started at {formatDateTime(run.started_at || run.created_at)}</div>
              <div>[INFO] Workflow: {workflowQuery.data?.name || run.workflow_id}</div>
              {timelineQuery.data?.map((event, i) => (
                <div key={i}>
                  [{formatDateTime(event.time)}] [{event.type.toUpperCase()}] {event.message || event.step_id ? `Step ${event.step_id}` : ""}
                </div>
              ))}
              {run.status === "failed" && (
                <div className="text-red-400">[ERROR] Run failed: {failureReason}</div>
              )}
              {run.status === "succeeded" && (
                <div>[INFO] Run completed successfully</div>
              )}
            </div>
          )}

          {activeTab === "Audit Log" && (
            <div className="space-y-3">
              {(timelineQuery.data || []).map((event) => (
                <div key={`${event.time}-${event.type}-audit`} className="rounded-2xl border border-border bg-white/70 p-3">
                  <div className="text-xs text-muted">{formatDateTime(event.time)}</div>
                  <div className="text-sm font-semibold text-ink">{event.type}</div>
                  {event.message ? <div className="text-xs text-muted">{event.message}</div> : null}
                  {event.data ? (
                    <pre className="mt-2 rounded-xl bg-white/70 p-2 text-[11px] text-ink">
                      {JSON.stringify(event.data, null, 2)}
                    </pre>
                  ) : null}
                </div>
              ))}
            </div>
          )}
        </div>
      </Card>

      <Drawer open={Boolean(selectedJobId)} onClose={() => setSelectedJobId(null)}>
        {selectedJobId ? (
          <div className="space-y-4">
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Job</div>
            <h3 className="text-xl font-semibold text-ink">{selectedJobId}</h3>
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                size="sm"
                type="button"
                onClick={() => navigate(`/jobs/${selectedJobId}`)}
              >
                Open job detail
              </Button>
              {jobQuery.data?.pack_id ? (
                <Button
                  variant="subtle"
                  size="sm"
                  type="button"
                  onClick={() => navigate(`/packs?pack_id=${jobQuery.data?.pack_id}`)}
                >
                  Open pack
                </Button>
              ) : null}
              {jobQuery.data ? (
                <Button
                  variant="subtle"
                  size="sm"
                  type="button"
                  onClick={() => {
                    const params = new URLSearchParams();
                    params.set("job_id", jobQuery.data?.id || selectedJobId);
                    if (jobQuery.data?.topic) {
                      params.set("topic", jobQuery.data.topic);
                    }
                    if (jobQuery.data?.tenant) {
                      params.set("tenant", jobQuery.data.tenant);
                    }
                    if (jobQuery.data?.capability) {
                      params.set("capability", jobQuery.data.capability);
                    }
                    if (jobQuery.data?.pack_id) {
                      params.set("pack_id", jobQuery.data.pack_id);
                    }
                    navigate(`/policy?${params.toString()}`);
                  }}
                >
                  Open policy
                </Button>
              ) : null}
            </div>
            {jobQuery.data?.approval_required ? (
              <div className="rounded-2xl border border-border bg-white/70 p-3">
                <div className="mb-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted">Approval</div>
                <div className="grid gap-2 lg:grid-cols-2">
                  <Input
                    value={approvalReason}
                    onChange={(event) => setApprovalReason(event.target.value)}
                    placeholder="Reason"
                  />
                  <Input
                    value={approvalNote}
                    onChange={(event) => setApprovalNote(event.target.value)}
                    placeholder="Note"
                  />
                </div>
                <div className="mt-2 flex flex-wrap gap-2">
                  <Button
                    variant="primary"
                    size="sm"
                    type="button"
                    onClick={() => approveJobMutation.mutate(selectedJobId)}
                    disabled={approveJobMutation.isPending}
                  >
                    Approve
                  </Button>
                  <Button
                    variant="danger"
                    size="sm"
                    type="button"
                    onClick={() => rejectJobMutation.mutate(selectedJobId)}
                    disabled={rejectJobMutation.isPending}
                  >
                    Reject
                  </Button>
                </div>
              </div>
            ) : null}
            {jobQuery.isLoading ? (
              <div className="text-sm text-muted">Loading job details...</div>
            ) : jobQuery.data ? (
              <div className="rounded-2xl border border-border bg-white/70 p-4 text-xs text-muted">
                <pre>{JSON.stringify(jobQuery.data, null, 2)}</pre>
              </div>
            ) : (
              <div className="text-sm text-muted">No job details found.</div>
            )}
          </div>
        ) : null}
      </Drawer>
    </div>
  );
}
