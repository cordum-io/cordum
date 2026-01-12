import { useMemo, useState } from "react";
import { useMutation, useQueryClient, useQueries } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { api } from "../lib/api";
import { formatRelative } from "../lib/format";
import { useWorkflows } from "../hooks/useWorkflows";
import { usePinStore } from "../state/pins";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Textarea } from "../components/ui/Textarea";
import { Drawer } from "../components/ui/Drawer";
import { WorkflowBuilder } from "../components/workflow/WorkflowBuilder";
import type { Workflow } from "../types/api";

export function WorkflowsPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const workflowsQuery = useWorkflows();
  const pinStart = usePinStore((state) => state.addPin);

  const runQueries = useQueries({
    queries:
      workflowsQuery.data?.map((workflow) => ({
        queryKey: ["runs", workflow.id],
        queryFn: () => api.listRunsByWorkflow(workflow.id),
      })) ?? [],
  });

  const runStats = useMemo(() => {
    const map = new Map<string, { total: number; success: number; cost: number }>();
    workflowsQuery.data?.forEach((workflow, index) => {
      const runs = runQueries[index]?.data || [];
      const success = runs.filter((run) => run.status === "succeeded").length;
      const cost = runs.reduce((sum, run) => sum + (run.total_cost || 0), 0);
      map.set(workflow.id, { total: runs.length, success, cost });
    });
    return map;
  }, [runQueries, workflowsQuery.data]);

  const [selectedWorkflow, setSelectedWorkflow] = useState<Workflow | null>(null);
  const [payload, setPayload] = useState("{}" as string);
  const [payloadError, setPayloadError] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [viewMode, setViewMode] = useState<"json" | "visual">("json");
  const [createPayload, setCreatePayload] = useState(`{\n  \"id\": \"incident-triage\",\n  \"org_id\": \"default\",\n  \"name\": \"Incident Triage\",\n  \"version\": \"1.0.0\",\n  \"timeout_sec\": 900,\n  \"created_by\": \"ops\",\n  \"steps\": {\n    \"ingest\": {\n      \"name\": \"Ingest\",\n      \"type\": \"worker\",\n      \"topic\": \"job.default\"\n    },\n    \"approve\": {\n      \"name\": \"Approval\",\n      \"type\": \"approval\",\n      \"depends_on\": [\"ingest\"]\n    }\n  }\n}`);
  const [createError, setCreateError] = useState<string | null>(null);

  const startMutation = useMutation({
    mutationFn: ({ workflow, body }: { workflow: Workflow; body: Record<string, unknown> }) =>
      api.startRun(workflow.id, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["runs"] });
      setSelectedWorkflow(null);
    },
    onError: (error: Error) => setPayloadError(error.message),
  });

  const createMutation = useMutation({
    mutationFn: (body: Record<string, unknown>) => api.createWorkflow(body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["workflows"] });
      setCreateOpen(false);
    },
    onError: (error: Error) => setCreateError(error.message),
  });

  const handleBuilderChange = (workflow: Partial<Workflow>) => {
    setCreatePayload(JSON.stringify(workflow, null, 2));
  };

  const currentWorkflowData = useMemo(() => {
    try {
      return JSON.parse(createPayload);
    } catch {
      return {};
    }
  }, [createPayload]);

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Workflows</CardTitle>
          <Button variant="subtle" size="sm" type="button" onClick={() => setCreateOpen(true)}>
            Create workflow
          </Button>
        </CardHeader>
        {workflowsQuery.isLoading ? (
          <div className="text-sm text-muted">Loading workflows...</div>
        ) : workflowsQuery.data?.length ? (
          <div className="space-y-3">
            {workflowsQuery.data.map((workflow) => {
              const stats = runStats.get(workflow.id);
              const successRate = stats && stats.total > 0 ? Math.round((stats.success / stats.total) * 100) : 0;
              return (
                <div key={workflow.id} className="rounded-2xl border border-border bg-white/70 p-4">
                  <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
                    <div>
                      <div className="text-sm font-semibold text-ink">{workflow.name || workflow.id}</div>
                      <div className="text-xs text-muted">{workflow.description || "No description"}</div>
                      <div className="text-[11px] text-muted">Updated {formatRelative(workflow.updated_at)}</div>
                    </div>
                    <div className="flex flex-wrap gap-2">
                      <Button variant="outline" size="sm" type="button" onClick={() => navigate(`/workflows/${workflow.id}`)}>
                        Details
                      </Button>
                      <Button variant="outline" size="sm" type="button" onClick={() => pinStart({ id: workflow.id, label: workflow.name || workflow.id, type: "workflow" })}>
                        Pin
                      </Button>
                      <Button variant="primary" size="sm" type="button" onClick={() => setSelectedWorkflow(workflow)}>
                        Start run
                      </Button>
                    </div>
                  </div>
                  <div className="mt-3 text-xs text-muted">
                    {stats ? `${stats.total} runs · ${successRate}% success · $${stats.cost.toFixed(4)} est. cost` : "No runs yet"}
                  </div>
                </div>
              );
            })}
          </div>
        ) : (
          <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
            No workflows are registered yet.
          </div>
        )}
      </Card>

      <Drawer open={Boolean(selectedWorkflow)} onClose={() => setSelectedWorkflow(null)}>
        {selectedWorkflow ? (
          <div className="space-y-4">
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Start Run</div>
            <h3 className="text-xl font-semibold text-ink">{selectedWorkflow.name || selectedWorkflow.id}</h3>
            <div className="text-xs text-muted">Provide JSON input for this workflow.</div>
            <Textarea
              rows={10}
              value={payload}
              onChange={(event) => setPayload(event.target.value)}
            />
            {payloadError ? <div className="text-xs text-danger">{payloadError}</div> : null}
            <div className="flex gap-2">
              <Button
                variant="primary"
                type="button"
                onClick={() => {
                  setPayloadError(null);
                  try {
                    const body = JSON.parse(payload || "{}");
                    startMutation.mutate({ workflow: selectedWorkflow, body });
                  } catch (error) {
                    setPayloadError(error instanceof Error ? error.message : "Invalid JSON");
                  }
                }}
                disabled={startMutation.isPending}
              >
                Launch run
              </Button>
              <Button variant="outline" type="button" onClick={() => setSelectedWorkflow(null)}>
                Close
              </Button>
            </div>
          </div>
        ) : null}
      </Drawer>

      <Drawer open={createOpen} onClose={() => setCreateOpen(false)} size="lg">
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <div className="text-xs uppercase tracking-[0.2em] text-muted">Create Workflow</div>
              <h3 className="text-xl font-semibold text-ink">Design workflow</h3>
            </div>
            <div className="flex gap-2">
              <Button size="xs" variant={viewMode === "json" ? "primary" : "outline"} onClick={() => setViewMode("json")}>JSON</Button>
              <Button size="xs" variant={viewMode === "visual" ? "primary" : "outline"} onClick={() => setViewMode("visual")}>Visual</Button>
            </div>
          </div>

          {viewMode === "json" ? (
            <Textarea rows={16} value={createPayload} onChange={(event) => setCreatePayload(event.target.value)} />
          ) : (
            <WorkflowBuilder initialWorkflow={currentWorkflowData} onChange={handleBuilderChange} />
          )}

          {createError ? <div className="text-xs text-danger">{createError}</div> : null}
          <div className="flex gap-2">
            <Button
              variant="primary"
              type="button"
              onClick={() => {
                setCreateError(null);
                try {
                  const body = JSON.parse(createPayload || "{}");
                  createMutation.mutate(body);
                } catch (error) {
                  setCreateError(error instanceof Error ? error.message : "Invalid JSON");
                }
              }}
              disabled={createMutation.isPending}
            >
              Save workflow
            </Button>
            <Button variant="outline" type="button" onClick={() => setCreateOpen(false)}>
              Close
            </Button>
          </div>
        </div>
      </Drawer>
    </div>
  );
}
