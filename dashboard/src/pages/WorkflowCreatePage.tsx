import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Code, Workflow } from "lucide-react";
import { api } from "../lib/api";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Textarea } from "../components/ui/Textarea";
import { WorkflowBuilder } from "../components/workflow/WorkflowBuilder";
import type { Workflow as WorkflowType } from "../types/api";

const defaultWorkflow = {
  id: "",
  org_id: "default",
  team_id: "default",
  name: "",
  description: "",
  version: "1.0.0",
  timeout_sec: 900,
  steps: {},
};

export function WorkflowCreatePage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const [viewMode, setViewMode] = useState<"visual" | "json">("visual");
  const [workflowData, setWorkflowData] = useState<Partial<WorkflowType>>(defaultWorkflow);
  const [jsonPayload, setJsonPayload] = useState(JSON.stringify(defaultWorkflow, null, 2));
  const [error, setError] = useState<string | null>(null);

  const createMutation = useMutation({
    mutationFn: (body: Record<string, unknown>) => api.createWorkflow(body),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["workflows"] });
      navigate(`/workflows/${data.id}`);
    },
    onError: (err: Error) => setError(err.message),
  });

  const handleBuilderChange = (workflow: Partial<WorkflowType>) => {
    setWorkflowData(workflow);
    setJsonPayload(JSON.stringify(workflow, null, 2));
  };

  const handleJsonChange = (json: string) => {
    setJsonPayload(json);
    try {
      const parsed = JSON.parse(json);
      setWorkflowData(parsed);
      setError(null);
    } catch {
      // Don't update workflowData if JSON is invalid
    }
  };

  const handleMetaChange = (field: keyof WorkflowType, value: string | number) => {
    const updated = { ...workflowData, [field]: value };
    setWorkflowData(updated);
    setJsonPayload(JSON.stringify(updated, null, 2));
  };

  const handleSave = () => {
    setError(null);
    try {
      const body = viewMode === "json" ? JSON.parse(jsonPayload) : workflowData;
      if (!body.id) {
        setError("Workflow ID is required");
        return;
      }
      if (!body.name) {
        setError("Workflow name is required");
        return;
      }
      createMutation.mutate(body);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Invalid JSON");
    }
  };

  const currentWorkflowForBuilder = useMemo(() => {
    try {
      return viewMode === "json" ? JSON.parse(jsonPayload) : workflowData;
    } catch {
      return workflowData;
    }
  }, [viewMode, jsonPayload, workflowData]);

  return (
    <div className="space-y-6">
      {/* Header */}
      <Card>
        <CardHeader>
          <div className="flex items-center gap-4">
            <Button variant="ghost" size="sm" onClick={() => navigate("/workflows")}>
              <ArrowLeft className="h-4 w-4" />
            </Button>
            <CardTitle>Create Workflow</CardTitle>
          </div>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant={viewMode === "visual" ? "primary" : "outline"}
              onClick={() => setViewMode("visual")}
            >
              <Workflow className="h-4 w-4 mr-2" />
              Visual
            </Button>
            <Button
              size="sm"
              variant={viewMode === "json" ? "primary" : "outline"}
              onClick={() => setViewMode("json")}
            >
              <Code className="h-4 w-4 mr-2" />
              JSON
            </Button>
          </div>
        </CardHeader>
      </Card>

      {/* Metadata */}
      <Card>
        <div className="grid gap-4 lg:grid-cols-4">
          <div>
            <label className="text-xs uppercase tracking-[0.2em] text-muted block mb-2">
              Workflow ID *
            </label>
            <Input
              value={workflowData.id || ""}
              onChange={(e) => handleMetaChange("id", e.target.value)}
              placeholder="my-workflow"
            />
          </div>
          <div>
            <label className="text-xs uppercase tracking-[0.2em] text-muted block mb-2">
              Name *
            </label>
            <Input
              value={workflowData.name || ""}
              onChange={(e) => handleMetaChange("name", e.target.value)}
              placeholder="My Workflow"
            />
          </div>
          <div>
            <label className="text-xs uppercase tracking-[0.2em] text-muted block mb-2">
              Version
            </label>
            <Input
              value={workflowData.version || "1.0.0"}
              onChange={(e) => handleMetaChange("version", e.target.value)}
              placeholder="1.0.0"
            />
          </div>
          <div>
            <label className="text-xs uppercase tracking-[0.2em] text-muted block mb-2">
              Timeout (sec)
            </label>
            <Input
              type="number"
              value={workflowData.timeout_sec || 900}
              onChange={(e) => {
                const parsed = Number.parseInt(e.target.value, 10);
                handleMetaChange("timeout_sec", Number.isFinite(parsed) ? parsed : 900);
              }}
              placeholder="900"
            />
          </div>
        </div>
        <div className="mt-4">
          <label className="text-xs uppercase tracking-[0.2em] text-muted block mb-2">
            Description
          </label>
          <Input
            value={workflowData.description || ""}
            onChange={(e) => handleMetaChange("description", e.target.value)}
            placeholder="Optional description of this workflow"
          />
        </div>
      </Card>

      {/* Builder / JSON Editor */}
      <Card>
        <CardHeader>
          <CardTitle>
            {viewMode === "visual" ? "Workflow Steps" : "JSON Definition"}
          </CardTitle>
        </CardHeader>

        {viewMode === "visual" ? (
          <WorkflowBuilder
            initialWorkflow={currentWorkflowForBuilder}
            onChange={handleBuilderChange}
            height={600}
          />
        ) : (
          <Textarea
            rows={24}
            value={jsonPayload}
            onChange={(e) => handleJsonChange(e.target.value)}
            className="font-mono text-sm"
            placeholder="Paste or edit workflow JSON..."
          />
        )}
      </Card>

      {/* Actions */}
      <Card>
        <div className="flex items-center justify-between">
          <div>
            {error && <div className="text-sm text-danger">{error}</div>}
          </div>
          <div className="flex gap-3">
            <Button variant="outline" onClick={() => navigate("/workflows")}>
              Cancel
            </Button>
            <Button
              variant="primary"
              onClick={handleSave}
              disabled={createMutation.isPending}
            >
              {createMutation.isPending ? "Creating..." : "Create Workflow"}
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}
