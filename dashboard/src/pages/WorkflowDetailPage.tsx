import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { api } from "../lib/api";
import { formatRelative } from "../lib/format";
import { useRunsByWorkflow, useWorkflows } from "../hooks/useWorkflows";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Textarea } from "../components/ui/Textarea";
import { RunStatusBadge } from "../components/StatusBadge";
import { WorkflowCanvas } from "../components/workflow/WorkflowCanvas";

export function WorkflowDetailPage() {
  const { workflowId } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const workflowsQuery = useWorkflows();
  const workflow = workflowsQuery.data?.find((item) => item.id === workflowId);
  const runsQuery = useRunsByWorkflow(workflowId);

  const [payload, setPayload] = useState("{}");
  const [payloadError, setPayloadError] = useState<string | null>(null);

  const startMutation = useMutation({
    mutationFn: (body: Record<string, unknown>) => api.startRun(workflowId as string, body),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["runs", workflowId] }),
    onError: (error: Error) => setPayloadError(error.message),
  });

  if (workflowsQuery.isLoading) {
    return <div className="text-sm text-muted">Loading...</div>;
  }
  if (!workflow) {
    return <div className="text-sm text-muted">Workflow not found.</div>;
  }

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>{workflow.name || workflow.id}</CardTitle>
          <Button variant="outline" size="sm" type="button" onClick={() => navigate("/workflows")}>Back</Button>
        </CardHeader>
        <div className="grid gap-4 lg:grid-cols-3">
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Version</div>
            <div className="text-sm font-semibold text-ink">{workflow.version || "-"}</div>
          </div>
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Org</div>
            <div className="text-sm font-semibold text-ink">{workflow.org_id || "default"}</div>
          </div>
          <div>
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Updated</div>
            <div className="text-sm font-semibold text-ink">{formatRelative(workflow.updated_at)}</div>
          </div>
        </div>
        <div className="mt-4 text-sm text-muted">{workflow.description || "No description"}</div>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Workflow Canvas</CardTitle>
        </CardHeader>
        <WorkflowCanvas workflow={workflow} height={420} />
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Start New Run</CardTitle>
        </CardHeader>
        <div className="space-y-3">
          <Textarea rows={8} value={payload} onChange={(event) => setPayload(event.target.value)} />
          {payloadError ? <div className="text-xs text-danger">{payloadError}</div> : null}
          <Button
            variant="primary"
            type="button"
            onClick={() => {
              setPayloadError(null);
              try {
                const body = JSON.parse(payload || "{}");
                startMutation.mutate(body);
              } catch (error) {
                setPayloadError(error instanceof Error ? error.message : "Invalid JSON");
              }
            }}
            disabled={startMutation.isPending}
          >
            Launch run
          </Button>
        </div>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Recent Runs</CardTitle>
        </CardHeader>
        {runsQuery.data?.length ? (
          <div className="space-y-3">
            {runsQuery.data.map((run) => (
              <div key={run.id} className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="flex flex-col gap-2 lg:flex-row lg:items-center lg:justify-between">
                  <div>
                    <div className="text-sm font-semibold text-ink">Run {run.id.slice(0, 8)}</div>
                    <div className="text-xs text-muted">Updated {formatRelative(run.updated_at || run.created_at)}</div>
                  </div>
                  <RunStatusBadge status={run.status} />
                </div>
              </div>
            ))}
          </div>
        ) : (
          <div className="text-sm text-muted">No runs yet.</div>
        )}
      </Card>
    </div>
  );
}
