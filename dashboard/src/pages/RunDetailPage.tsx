import { useEffect, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { formatDateTime, formatDuration } from "../lib/format";
import { useEventStore } from "../state/events";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { ApprovalStatusBadge, RunStatusBadge } from "../components/StatusBadge";
import { Drawer } from "../components/ui/Drawer";
import { Input } from "../components/ui/Input";
import { WorkflowCanvas } from "../components/workflow/WorkflowCanvas";
import type { ApprovalItem, JobDetail } from "../types/api";

const tabs = ["Overview", "Timeline", "DAG", "Input/Output", "Jobs", "Audit Log"] as const;

export function RunDetailPage() {
  const { runId } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState<(typeof tabs)[number]>("Overview");
  const [selectedJobId, setSelectedJobId] = useState<string | null>(null);
  const [approvalReason, setApprovalReason] = useState("");
  const [approvalNote, setApprovalNote] = useState("");

  const runQuery = useQuery({
    queryKey: ["run", runId],
    queryFn: () => api.getRun(runId as string),
    enabled: Boolean(runId),
  });

  const workflowQuery = useQuery({
    queryKey: ["workflow", runQuery.data?.workflow_id],
    queryFn: () => api.getWorkflow(runQuery.data?.workflow_id as string),
    enabled: Boolean(runQuery.data?.workflow_id),
  });

  const timelineQuery = useQuery({
    queryKey: ["timeline", runId],
    queryFn: () => api.getRunTimeline(runId as string),
    enabled: Boolean(runId),
  });

  const approvalsQuery = useQuery({
    queryKey: ["approvals", "run", runId],
    queryFn: () => api.listApprovals(200),
    enabled: Boolean(runId),
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
    mutationFn: (id: string) => api.rerunRun(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["run", runId] }),
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

  if (runQuery.isLoading) {
    return <div className="text-sm text-muted">Loading run details...</div>;
  }
  if (runQuery.isError || !runQuery.data) {
    return <div className="text-sm text-muted">Run not found.</div>;
  }

  const run = runQuery.data;
  const runSteps = Object.values(run.steps || {});
  const runApprovals = runSteps
    .map((step) => (step.job_id ? approvalsByJob.get(step.job_id) : undefined))
    .filter((item): item is ApprovalItem => Boolean(item));
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
              onClick={() => rerunMutation.mutate(run.id)}
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
              onClick={() => rerunMutation.mutate(run.id)}
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

      <Card>
        <div className="flex flex-wrap gap-2 border-b border-border pb-4">
          {tabs.map((tab) => (
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
              {runApprovals.length ? (
                <div className="rounded-2xl border border-border bg-white/70 p-4">
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
              ) : null}
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
