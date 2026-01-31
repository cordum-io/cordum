import { useEffect, useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { Database, ClipboardList, Layers } from "lucide-react";
import { api } from "../lib/api";
import { formatDateTime, formatDuration } from "../lib/format";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Badge } from "../components/ui/Badge";

const statusVariant = (status?: string): "default" | "success" | "warning" | "danger" | "info" => {
  if (!status) {
    return "default";
  }
  const normalized = status.toLowerCase();
  if (["failed", "timed_out", "cancelled", "blocked", "denied"].includes(normalized)) {
    return "danger";
  }
  if (["running", "waiting", "pending", "queued"].includes(normalized)) {
    return "warning";
  }
  if (["succeeded", "success", "completed"].includes(normalized)) {
    return "success";
  }
  return "info";
};

export function ContextInspectorPage() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [runId, setRunId] = useState(searchParams.get("run_id") || "");
  const [jobId, setJobId] = useState(searchParams.get("job_id") || "");
  const runIdValue = runId.trim();
  const jobIdValue = jobId.trim();
  const runIdValid = runIdValue.length >= 20;
  const jobIdValid = jobIdValue.length >= 20;

  useEffect(() => {
    setRunId(searchParams.get("run_id") || "");
    setJobId(searchParams.get("job_id") || "");
  }, [searchParams]);

  const runQuery = useQuery({
    queryKey: ["run", runIdValue],
    queryFn: () => api.getRun(runIdValue),
    enabled: runIdValid,
  });

  const jobQuery = useQuery({
    queryKey: ["job", jobIdValue],
    queryFn: () => api.getJob(jobIdValue),
    enabled: jobIdValid,
  });

  const handleLoad = () => {
    const params = new URLSearchParams();
    if (runIdValue) {
      params.set("run_id", runIdValue);
    }
    if (jobIdValue) {
      params.set("job_id", jobIdValue);
    }
    setSearchParams(params, { replace: true });
  };

  const run = runQuery.data;
  const job = jobQuery.data;
  const steps = useMemo(() => Object.values(run?.steps || {}), [run]);
  const activeStep = steps.find((step) => ["running", "waiting", "pending"].includes(step.status));
  const contextPayload = run?.context && Object.keys(run.context).length > 0 ? run.context : run?.input;

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Context Inspector</CardTitle>
          <div className="text-xs text-muted">Trace run context, job memory, and step-by-step state.</div>
        </CardHeader>
        <div className="grid gap-3 lg:grid-cols-[1fr_1fr_auto]">
          <Input value={runId} onChange={(event) => setRunId(event.target.value)} placeholder="Run ID" />
          <Input value={jobId} onChange={(event) => setJobId(event.target.value)} placeholder="Job ID (optional)" />
          <Button variant="primary" type="button" onClick={handleLoad}>
            Load
          </Button>
        </div>
        <div className="mt-3 flex flex-wrap gap-2 text-xs text-muted">
          {runIdValue && !runIdValid ? <div>Run ID looks shortened. Use the full ID from Runs.</div> : null}
          {jobIdValue && !jobIdValid ? <div>Job ID looks shortened. Use the full ID from Jobs.</div> : null}
          {runIdValid ? (
            <Button variant="outline" size="sm" type="button" onClick={() => navigate(`/runs/${runIdValue}`)}>
              Open Mission Control
            </Button>
          ) : null}
          {jobIdValid ? (
            <Button variant="outline" size="sm" type="button" onClick={() => navigate(`/jobs/${jobIdValue}`)}>
              Open Job Detail
            </Button>
          ) : null}
        </div>
      </Card>

      <div className="grid gap-6 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <Card>
          <CardHeader>
            <CardTitle>Context Window</CardTitle>
            <div className="text-xs text-muted">Run context + input snapshot</div>
          </CardHeader>
          {runQuery.isLoading ? (
            <div className="text-sm text-muted">Loading run context...</div>
          ) : run ? (
            <div className="space-y-4">
              <div className="rounded-2xl border border-border bg-white/70 p-3 text-xs text-muted">
                <pre>{JSON.stringify(contextPayload || {}, null, 2)}</pre>
              </div>
              <div className="grid gap-3 lg:grid-cols-2">
                <div className="rounded-2xl border border-border bg-white/70 p-3">
                  <div className="text-xs uppercase tracking-[0.2em] text-muted">Status</div>
                  <div className="mt-1 text-sm font-semibold text-ink">{run.status}</div>
                </div>
                <div className="rounded-2xl border border-border bg-white/70 p-3">
                  <div className="text-xs uppercase tracking-[0.2em] text-muted">Duration</div>
                  <div className="mt-1 text-sm font-semibold text-ink">
                    {formatDuration(run.started_at || run.created_at, run.completed_at)}
                  </div>
                </div>
                <div className="rounded-2xl border border-border bg-white/70 p-3">
                  <div className="text-xs uppercase tracking-[0.2em] text-muted">Workflow</div>
                  <div className="mt-1 text-sm font-semibold text-ink">{run.workflow_id}</div>
                </div>
                <div className="rounded-2xl border border-border bg-white/70 p-3">
                  <div className="text-xs uppercase tracking-[0.2em] text-muted">Active Step</div>
                  <div className="mt-1 text-sm font-semibold text-ink">{activeStep?.step_id || "-"}</div>
                </div>
              </div>
            </div>
          ) : (
            <div className="text-sm text-muted">Provide a run ID to inspect context.</div>
          )}
        </Card>

        <div className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle>Execution Timeline</CardTitle>
              <div className="text-xs text-muted">Steps and state transitions</div>
            </CardHeader>
            {steps.length === 0 ? (
              <div className="rounded-2xl border border-dashed border-border p-4 text-sm text-muted">
                No steps recorded yet.
              </div>
            ) : (
              <div className="space-y-2">
                {steps.map((step) => (
                  <div key={step.step_id} className="rounded-2xl border border-border bg-white/70 p-3">
                    <div className="flex items-center justify-between">
                      <div>
                        <div className="text-sm font-semibold text-ink">{step.step_id}</div>
                        <div className="text-[11px] text-muted">
                          {step.started_at ? `Started ${formatDateTime(step.started_at)}` : "Pending"}
                        </div>
                      </div>
                      <Badge variant={statusVariant(step.status)}>{step.status}</Badge>
                    </div>
                    {step.job_id ? <div className="mt-2 text-[10px] text-muted">Job {step.job_id}</div> : null}
                  </div>
                ))}
              </div>
            )}
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Job Context</CardTitle>
              <div className="text-xs text-muted">Memory pointers and outputs</div>
            </CardHeader>
            {jobQuery.isLoading ? (
              <div className="text-sm text-muted">Loading job details...</div>
            ) : job ? (
              <div className="space-y-3">
                <div className="rounded-2xl border border-border bg-white/70 p-3">
                  <div className="text-xs uppercase tracking-[0.2em] text-muted">Job</div>
                  <div className="mt-1 text-sm font-semibold text-ink">{job.id}</div>
                  <div className="text-[11px] text-muted">{job.topic || "-"}</div>
                </div>
                <div className="rounded-2xl border border-border bg-white/70 p-3">
                  <div className="text-xs uppercase tracking-[0.2em] text-muted">Context Pointer</div>
                  <div className="mt-1 text-[11px] text-ink break-all">{job.context_ptr || "-"}</div>
                </div>
                <div className="rounded-2xl border border-border bg-white/70 p-3">
                  <div className="text-xs uppercase tracking-[0.2em] text-muted">Result Pointer</div>
                  <div className="mt-1 text-[11px] text-ink break-all">{job.result_ptr || "-"}</div>
                </div>
                <div className="rounded-2xl border border-border bg-white/70 p-3 text-xs text-muted">
                  <div className="mb-2 flex items-center gap-2 text-[10px] uppercase tracking-[0.2em]">
                    <Database className="h-3 w-3" />
                    Context Payload
                  </div>
                  <pre>{JSON.stringify(job.context || {}, null, 2)}</pre>
                </div>
                <div className="rounded-2xl border border-border bg-white/70 p-3 text-xs text-muted">
                  <div className="mb-2 flex items-center gap-2 text-[10px] uppercase tracking-[0.2em]">
                    <ClipboardList className="h-3 w-3" />
                    Result Payload
                  </div>
                  <pre>{JSON.stringify(job.result || job.error_message || {}, null, 2)}</pre>
                </div>
              </div>
            ) : (
              <div className="rounded-2xl border border-dashed border-border p-4 text-sm text-muted">
                Provide a job ID to inspect job context.
              </div>
            )}
          </Card>
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Context Checklist</CardTitle>
          <div className="text-xs text-muted">Quick inspection cues before debugging</div>
        </CardHeader>
        <div className="grid gap-3 lg:grid-cols-3">
          <div className="rounded-2xl border border-border bg-white/70 p-3">
            <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted">
              <Layers className="h-3 w-3" />
              Inputs present
            </div>
            <div className="mt-2 text-sm font-semibold text-ink">
              {contextPayload && Object.keys(contextPayload).length > 0 ? "Yes" : "No"}
            </div>
          </div>
          <div className="rounded-2xl border border-border bg-white/70 p-3">
            <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted">
              <Layers className="h-3 w-3" />
              Steps tracked
            </div>
            <div className="mt-2 text-sm font-semibold text-ink">{steps.length}</div>
          </div>
          <div className="rounded-2xl border border-border bg-white/70 p-3">
            <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.2em] text-muted">
              <Layers className="h-3 w-3" />
              Active job
            </div>
            <div className="mt-2 text-sm font-semibold text-ink">{job ? "Loaded" : "-"}</div>
          </div>
        </div>
      </Card>
    </div>
  );
}
