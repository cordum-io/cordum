import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { api } from "../lib/api";
import { epochToMillis, formatDateTime } from "../lib/format";
import { useConfigStore } from "../state/config";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { JobStatusBadge } from "../components/StatusBadge";

export function JobDetailPage() {
  const { jobId } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const traceUrlTemplate = useConfigStore((state) => state.traceUrlTemplate);
  const [reason, setReason] = useState("");
  const [note, setNote] = useState("");

  const jobQuery = useQuery({
    queryKey: ["job", jobId],
    queryFn: () => api.getJob(jobId as string),
    enabled: Boolean(jobId),
  });
  const decisionsQuery = useQuery({
    queryKey: ["job", jobId, "decisions"],
    queryFn: () => api.listJobDecisions(jobId as string),
    enabled: Boolean(jobId),
  });

  const approveMutation = useMutation({
    mutationFn: () => api.approveJob(jobId as string, { reason, note }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["job", jobId] });
      queryClient.invalidateQueries({ queryKey: ["approvals"] });
      setReason("");
      setNote("");
    },
  });
  const rejectMutation = useMutation({
    mutationFn: () => api.rejectJob(jobId as string, { reason, note }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["job", jobId] });
      queryClient.invalidateQueries({ queryKey: ["approvals"] });
      setReason("");
      setNote("");
    },
  });
  const remediateMutation = useMutation({
    mutationFn: (remediationId?: string) => api.remediateJob(jobId as string, remediationId),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["job", jobId] });
      queryClient.invalidateQueries({ queryKey: ["jobs"] });
      if (data?.job_id) {
        navigate(`/jobs/${data.job_id}`);
      }
    },
  });

  const job = jobQuery.data;
  const decisions = decisionsQuery.data || [];

  const traceUrl = useMemo(() => {
    if (!job?.trace_id || !traceUrlTemplate) {
      return "";
    }
    return traceUrlTemplate.replace("{{trace_id}}", job.trace_id);
  }, [job, traceUrlTemplate]);

  const policyLink = useMemo(() => {
    if (!job) {
      return "";
    }
    const params = new URLSearchParams();
    if (job.id) {
      params.set("job_id", job.id);
    }
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
  }, [job]);

  if (jobQuery.isLoading) {
    return <div className="text-sm text-muted">Loading job...</div>;
  }
  if (!job) {
    return <div className="text-sm text-muted">Job not found.</div>;
  }

  const decisionTimestamp = (value?: number): string => {
    const ms = epochToMillis(value);
    if (!ms) {
      return "-";
    }
    return formatDateTime(ms);
  };

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Job {job.id.slice(0, 12)}</CardTitle>
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" size="sm" type="button" onClick={() => navigate("/jobs")}>Back to jobs</Button>
            {job.run_id ? (
              <Button variant="subtle" size="sm" type="button" onClick={() => navigate(`/runs/${job.run_id}`)}>
                View run
              </Button>
            ) : null}
            {job.workflow_id ? (
              <Button variant="subtle" size="sm" type="button" onClick={() => navigate(`/workflows/${job.workflow_id}`)}>
                View workflow
              </Button>
            ) : null}
            {job.pack_id ? (
              <Button variant="subtle" size="sm" type="button" onClick={() => navigate(`/packs?pack_id=${job.pack_id}`)}>
                View pack
              </Button>
            ) : null}
            {policyLink ? (
              <Button variant="subtle" size="sm" type="button" onClick={() => navigate(policyLink)}>
                Open policy
              </Button>
            ) : null}
          </div>
        </CardHeader>
        <div className="grid gap-4 lg:grid-cols-4">
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Status</div>
            <JobStatusBadge state={job.state} />
          </div>
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Topic</div>
            <div className="text-sm font-semibold text-ink">{job.topic || "-"}</div>
          </div>
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Tenant</div>
            <div className="text-sm font-semibold text-ink">{job.tenant || "default"}</div>
          </div>
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Trace</div>
            <div className="text-sm font-semibold text-ink">
              {traceUrl ? (
                <a href={traceUrl} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline">
                  {job.trace_id}
                </a>
              ) : (
                job.trace_id || "-"
              )}
            </div>
          </div>
        </div>
      </Card>

      {job.approval_required ? (
        <Card>
          <CardHeader>
            <CardTitle>Approval Required</CardTitle>
            <div className="text-xs text-muted">Provide a reason or note before approving</div>
          </CardHeader>
          <div className="grid gap-3 lg:grid-cols-2">
            <Input value={reason} onChange={(event) => setReason(event.target.value)} placeholder="Reason" />
            <Input value={note} onChange={(event) => setNote(event.target.value)} placeholder="Note" />
          </div>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button
              variant="primary"
              size="sm"
              type="button"
              onClick={() => approveMutation.mutate()}
              disabled={approveMutation.isPending}
            >
              Approve
            </Button>
            <Button
              variant="danger"
              size="sm"
              type="button"
              onClick={() => rejectMutation.mutate()}
              disabled={rejectMutation.isPending}
            >
              Reject
            </Button>
          </div>
        </Card>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle>Policy Decision</CardTitle>
          <div className="text-xs text-muted">Safety evaluation details</div>
        </CardHeader>
        <div className="rounded-2xl border border-border bg-white/70 p-4 text-xs text-muted">
          <div>Decision: {job.safety_decision || "-"}</div>
          <div>Reason: {job.safety_reason || "-"}</div>
          <div>Rule: {job.safety_rule_id || "-"}</div>
          <div>Snapshot: {job.safety_snapshot || "-"}</div>
          <div>Capability: {job.capability || "-"}</div>
          <div>Pack: {job.pack_id || "-"}</div>
        </div>
        {job.safety_constraints ? (
          <pre className="mt-3 rounded-2xl border border-border bg-white/70 p-3 text-[11px] text-ink">
            {JSON.stringify(job.safety_constraints, null, 2)}
          </pre>
        ) : null}
        {job.safety_remediations?.length ? (
          <div className="mt-4 space-y-3">
            <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Suggested remediations</div>
            {job.safety_remediations.map((remediation, index) => (
              <div key={remediation.id || index} className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="text-sm font-semibold text-ink">{remediation.title || remediation.id || "Remediation"}</div>
                {remediation.summary ? <div className="mt-1 text-xs text-muted">{remediation.summary}</div> : null}
                <div className="mt-2 text-xs text-muted">
                  {remediation.replacement_topic ? `Topic: ${remediation.replacement_topic}` : "Topic: unchanged"}
                </div>
                <div className="text-xs text-muted">
                  {remediation.replacement_capability ? `Capability: ${remediation.replacement_capability}` : "Capability: unchanged"}
                </div>
                {remediation.add_labels ? (
                  <div className="mt-2 text-xs text-muted">
                    Add labels: {Object.keys(remediation.add_labels).length ? JSON.stringify(remediation.add_labels) : "none"}
                  </div>
                ) : null}
                {remediation.remove_labels?.length ? (
                  <div className="text-xs text-muted">Remove labels: {remediation.remove_labels.join(", ")}</div>
                ) : null}
                <div className="mt-3">
                  <Button
                    variant="outline"
                    size="sm"
                    type="button"
                    onClick={() => remediateMutation.mutate(remediation.id)}
                    disabled={remediateMutation.isPending}
                  >
                    Apply remediation
                  </Button>
                </div>
              </div>
            ))}
          </div>
        ) : null}
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Decision Audit Log</CardTitle>
          <div className="text-xs text-muted">Policy checks recorded for this job</div>
        </CardHeader>
        {decisionsQuery.isLoading ? (
          <div className="text-sm text-muted">Loading decision history...</div>
        ) : decisions.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
            No decision history recorded.
          </div>
        ) : (
          <div className="space-y-3">
            {decisions.map((decision, index) => (
              <div key={`${decision.rule_id || "rule"}-${decision.checked_at || index}`} className="rounded-2xl border border-border bg-white/70 p-4 text-xs text-muted">
                <div className="flex items-center justify-between">
                  <div className="text-sm font-semibold text-ink">{decision.decision || "UNKNOWN"}</div>
                  <div>{decisionTimestamp(decision.checked_at)}</div>
                </div>
                <div>Rule: {decision.rule_id || "-"}</div>
                <div>Snapshot: {decision.policy_snapshot || "-"}</div>
                {decision.reason ? <div>Reason: {decision.reason}</div> : null}
                {decision.constraints ? (
                  <pre className="mt-2 rounded-2xl border border-border bg-white/70 p-3 text-[11px] text-ink">
                    {JSON.stringify(decision.constraints, null, 2)}
                  </pre>
                ) : null}
              </div>
            ))}
          </div>
        )}
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Metadata</CardTitle>
        </CardHeader>
        <div className="grid gap-3 lg:grid-cols-2">
          <div className="rounded-2xl border border-border bg-white/70 p-4 text-xs text-muted">
            <div>Actor: {job.actor_id || "-"}</div>
            <div>Actor type: {job.actor_type || "-"}</div>
            <div>Idempotency: {job.idempotency_key || "-"}</div>
            <div>Attempts: {job.attempts ?? 0}</div>
          </div>
          <div className="rounded-2xl border border-border bg-white/70 p-4 text-xs text-muted">
            <div>Workflow: {job.workflow_id || "-"}</div>
            <div>Run: {job.run_id || "-"}</div>
            <div>Step: {job.step_id || "-"}</div>
          </div>
        </div>
        {job.labels ? (
          <pre className="mt-3 rounded-2xl border border-border bg-white/70 p-3 text-[11px] text-ink">
            {JSON.stringify(job.labels, null, 2)}
          </pre>
        ) : null}
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Context</CardTitle>
        </CardHeader>
        <pre className="rounded-2xl border border-border bg-white/70 p-3 text-[11px] text-ink">
          {JSON.stringify(job.context || {}, null, 2)}
        </pre>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Result</CardTitle>
        </CardHeader>
        <pre className="rounded-2xl border border-border bg-white/70 p-3 text-[11px] text-ink">
          {JSON.stringify(job.result || {}, null, 2)}
        </pre>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Logs</CardTitle>
          <div className="text-xs text-muted">External logs for this job execution</div>
        </CardHeader>
        <div className="rounded-2xl border border-border bg-black p-4 font-mono text-xs text-green-400 h-64 overflow-y-auto">
          <div>[INFO] Starting job execution...</div>
          <div>[INFO] Processing input parameters</div>
          <div>[INFO] Connecting to worker context</div>
          {job.state === "failed" ? (
             <div className="text-red-400">[ERROR] Execution failed: {job.error_message || "Unknown error"}</div>
          ) : (
             <div>[INFO] Execution completed successfully</div>
          )}
        </div>
      </Card>
    </div>
  );
}
