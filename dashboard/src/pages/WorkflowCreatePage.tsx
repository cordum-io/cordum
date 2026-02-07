import { useCallback, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Upload, GitBranch } from "lucide-react";
import { Button } from "../components/ui/Button";
import { Textarea } from "../components/ui/Textarea";
import { Card } from "../components/ui/Card";
import { WorkflowBuilder } from "../components/workflow/WorkflowBuilder";
import { useCreateWorkflow } from "../hooks/useWorkflows";
import { cn } from "../lib/utils";
import type { Workflow } from "../api/types";

type CreateMode = "visual" | "import";

function parseDefinition(raw: string): Partial<Workflow> | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  try {
    return JSON.parse(trimmed) as Partial<Workflow>;
  } catch {
    // not JSON — ignore
  }
  return null;
}

function ImportPanel() {
  const navigate = useNavigate();
  const createWorkflow = useCreateWorkflow();
  const [raw, setRaw] = useState("");
  const [error, setError] = useState<string | null>(null);

  const handleImport = useCallback(() => {
    const definition = parseDefinition(raw);
    if (!definition) {
      setError("Invalid JSON. Paste a valid workflow JSON definition.");
      return;
    }
    if (!definition.name) {
      setError("Workflow must have a 'name' field.");
      return;
    }
    setError(null);
    createWorkflow.mutate(definition, {
      onSuccess: () => navigate("/workflows"),
      onError: (err) => setError(err.message),
    });
  }, [raw, createWorkflow, navigate]);

  const handleFile = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result === "string") {
        setRaw(reader.result);
        setError(null);
      }
    };
    reader.readAsText(file);
  }, []);

  return (
    <div className="mx-auto max-w-2xl space-y-4">
      <Card>
        <div className="space-y-4">
          <div>
            <h2 className="text-sm font-semibold text-ink">Import from JSON</h2>
            <p className="mt-1 text-xs text-muted">
              Paste a workflow JSON definition or upload a file.
            </p>
          </div>

          <Textarea
            value={raw}
            onChange={(e) => { setRaw(e.target.value); setError(null); }}
            placeholder='{"name": "my-workflow", "steps": [...], "timeout": 3600}'
            rows={16}
            className="font-mono text-xs"
          />

          {error && (
            <p className="text-xs text-danger">{error}</p>
          )}

          <div className="flex items-center justify-between">
            <label className="flex cursor-pointer items-center gap-2 text-xs text-accent hover:underline">
              <Upload className="h-3.5 w-3.5" />
              Upload file
              <input
                type="file"
                accept=".json,.yaml,.yml"
                onChange={handleFile}
                className="hidden"
              />
            </label>

            <Button
              size="sm"
              onClick={handleImport}
              disabled={!raw.trim() || createWorkflow.isPending}
            >
              {createWorkflow.isPending ? "Importing..." : "Import & Create"}
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}

export default function WorkflowCreatePage() {
  const [mode, setMode] = useState<CreateMode>("visual");

  return (
    <div className="space-y-4">
      {/* Mode toggle */}
      <div className="flex items-center justify-between">
        <div className="flex gap-1 rounded-full border border-border p-1 w-fit">
          <button
            type="button"
            className={cn(
              "flex items-center gap-2 rounded-full px-5 py-2 text-xs font-semibold uppercase tracking-widest transition",
              mode === "visual"
                ? "bg-accent/15 text-accent"
                : "text-muted hover:text-ink",
            )}
            onClick={() => setMode("visual")}
          >
            <GitBranch className="h-3.5 w-3.5" />
            Visual Builder
          </button>
          <button
            type="button"
            className={cn(
              "flex items-center gap-2 rounded-full px-5 py-2 text-xs font-semibold uppercase tracking-widest transition",
              mode === "import"
                ? "bg-accent/15 text-accent"
                : "text-muted hover:text-ink",
            )}
            onClick={() => setMode("import")}
          >
            <Upload className="h-3.5 w-3.5" />
            JSON Import
          </button>
        </div>
      </div>

      {mode === "visual" && <WorkflowBuilder />}
      {mode === "import" && <ImportPanel />}
    </div>
  );
}
