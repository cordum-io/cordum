import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../lib/api";
import { formatRelative } from "../lib/format";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Input } from "../components/ui/Input";
import { Button } from "../components/ui/Button";
import { JobStatusBadge, RunStatusBadge } from "../components/StatusBadge";
import type { JobRecord, PackRecord, Workflow, WorkflowRun } from "../types/api";

export function SearchPage() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const query = (searchParams.get("q") || "").trim();
  const enabled = query.length > 0;

  const runsQuery = useQuery({
    queryKey: ["search", "runs", query],
    queryFn: () => api.listWorkflowRuns({ limit: 80 }),
    enabled,
  });
  const workflowsQuery = useQuery({
    queryKey: ["search", "workflows", query],
    queryFn: () => api.listWorkflows(),
    enabled,
  });
  const packsQuery = useQuery({
    queryKey: ["search", "packs", query],
    queryFn: () => api.listPacks(),
    enabled,
  });
  const jobsQuery = useQuery({
    queryKey: ["search", "jobs", query],
    queryFn: () => api.listJobs({ limit: 80 }),
    enabled,
  });

  const runs = useMemo(() => {
    const q = query.toLowerCase();
    return (runsQuery.data?.items || [])
      .filter((run: WorkflowRun) =>
        [run.id, run.workflow_id, run.status, run.org_id, run.team_id]
          .filter(Boolean)
          .some((value) => String(value).toLowerCase().includes(q))
      )
      .slice(0, 8);
  }, [runsQuery.data, query]);

  const workflows = useMemo(() => {
    const q = query.toLowerCase();
    return (workflowsQuery.data || [])
      .filter((workflow: Workflow) =>
        [workflow.id, workflow.name, workflow.description]
          .filter(Boolean)
          .some((value) => String(value).toLowerCase().includes(q))
      )
      .slice(0, 8);
  }, [workflowsQuery.data, query]);

  const packs = useMemo(() => {
    const q = query.toLowerCase();
    return (packsQuery.data?.items || [])
      .filter((pack: PackRecord) =>
        [pack.id, pack.manifest?.metadata?.title, pack.manifest?.metadata?.description]
          .filter(Boolean)
          .some((value) => String(value).toLowerCase().includes(q))
      )
      .slice(0, 8);
  }, [packsQuery.data, query]);

  const jobs = useMemo(() => {
    const q = query.toLowerCase();
    return (jobsQuery.data?.items || [])
      .filter((job: JobRecord) =>
        [job.id, job.topic, job.tenant, job.team, job.pack_id, job.capability, job.trace_id]
          .filter(Boolean)
          .some((value) => String(value).toLowerCase().includes(q))
      )
      .slice(0, 8);
  }, [jobsQuery.data, query]);

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Global Search</CardTitle>
          <div className="text-xs text-muted">Search runs, workflows, packs, and jobs</div>
        </CardHeader>
        <Input
          value={query}
          onChange={(event) => {
            const next = event.target.value;
            setSearchParams(next ? { q: next } : {});
          }}
          placeholder="Search across Cordum"
        />
      </Card>

      {!enabled ? (
        <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
          Enter a search term to see results.
        </div>
      ) : (
        <div className="grid gap-6 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle>Runs</CardTitle>
              <div className="text-xs text-muted">Top matches</div>
            </CardHeader>
            {runs.length === 0 ? (
              <div className="text-sm text-muted">No matching runs.</div>
            ) : (
              <div className="space-y-3">
                {runs.map((run) => (
                  <div key={run.id} className="rounded-2xl border border-border bg-white/70 p-3">
                    <div className="flex items-center justify-between">
                      <div>
                        <div className="text-sm font-semibold text-ink">{run.workflow_id}</div>
                        <div className="text-xs text-muted">Run {run.id.slice(0, 8)}</div>
                      </div>
                      <RunStatusBadge status={run.status} />
                    </div>
                    <div className="mt-2 flex justify-between text-xs text-muted">
                      <span>Org {run.org_id || "default"}</span>
                      <Button variant="outline" size="sm" type="button" onClick={() => navigate(`/runs/${run.id}`)}>
                        Open
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Workflows</CardTitle>
              <div className="text-xs text-muted">Top matches</div>
            </CardHeader>
            {workflows.length === 0 ? (
              <div className="text-sm text-muted">No matching workflows.</div>
            ) : (
              <div className="space-y-3">
                {workflows.map((workflow) => (
                  <div key={workflow.id} className="rounded-2xl border border-border bg-white/70 p-3">
                    <div className="text-sm font-semibold text-ink">{workflow.name || workflow.id}</div>
                    <div className="text-xs text-muted">{workflow.description || "No description"}</div>
                    <div className="mt-2 flex justify-end">
                      <Button
                        variant="outline"
                        size="sm"
                        type="button"
                        onClick={() => navigate(`/workflows/${workflow.id}`)}
                      >
                        Open
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Packs</CardTitle>
              <div className="text-xs text-muted">Top matches</div>
            </CardHeader>
            {packs.length === 0 ? (
              <div className="text-sm text-muted">No matching packs.</div>
            ) : (
              <div className="space-y-3">
                {packs.map((pack) => (
                  <div key={pack.id} className="rounded-2xl border border-border bg-white/70 p-3">
                    <div className="text-sm font-semibold text-ink">{pack.manifest?.metadata?.title || pack.id}</div>
                    <div className="text-xs text-muted">{pack.manifest?.metadata?.description || "No description"}</div>
                    <div className="mt-2 flex justify-end">
                      <Button
                        variant="outline"
                        size="sm"
                        type="button"
                        onClick={() => navigate(`/packs?pack_id=${pack.id}`)}
                      >
                        Open
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Jobs</CardTitle>
              <div className="text-xs text-muted">Top matches</div>
            </CardHeader>
            {jobs.length === 0 ? (
              <div className="text-sm text-muted">No matching jobs.</div>
            ) : (
              <div className="space-y-3">
                {jobs.map((job) => (
                  <div key={job.id} className="rounded-2xl border border-border bg-white/70 p-3">
                    <div className="flex items-center justify-between">
                      <div>
                        <div className="text-sm font-semibold text-ink">Job {job.id.slice(0, 10)}</div>
                        <div className="text-xs text-muted">Topic {job.topic || "-"}</div>
                      </div>
                      <JobStatusBadge state={job.state} />
                    </div>
                    <div className="mt-2 flex items-center justify-between text-xs text-muted">
                      <span>Updated {formatRelative(jobUpdatedAt(job.updated_at))}</span>
                      <Button variant="outline" size="sm" type="button" onClick={() => navigate(`/jobs/${job.id}`)}>
                        Open
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Card>
        </div>
      )}
    </div>
  );
}

function jobUpdatedAt(updatedAt?: number): string {
  if (!updatedAt) {
    return "";
  }
  if (updatedAt > 1e14) {
    return new Date(Math.floor(updatedAt / 1000)).toISOString();
  }
  if (updatedAt > 1e11) {
    return new Date(updatedAt).toISOString();
  }
  return new Date(updatedAt * 1000).toISOString();
}
