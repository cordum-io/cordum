import { useState, useCallback, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { X, Loader, Play, Eye, AlertTriangle } from "lucide-react";
import { useWorkflow, useStartRun, useDryRun } from "../../hooks/useWorkflows";
import type { DryRunStepResult } from "../../hooks/useWorkflows";
import { SchemaForm } from "./SchemaForm";
import { Button } from "../ui/Button";
import { Badge } from "../ui/Badge";
import { Textarea } from "../ui/Textarea";
import { cn } from "../../lib/utils";

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface RunNowModalProps {
  workflowId: string;
  onClose: () => void;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function truncate(str: string, max: number): string {
  return str.length > max ? str.slice(0, max) + "\u2026" : str;
}

const DECISION_BADGE: Record<string, { variant: "success" | "danger" | "warning" | "default"; label: string }> = {
  allow: { variant: "success", label: "Allow" },
  deny: { variant: "danger", label: "Deny" },
  require_approval: { variant: "warning", label: "Require Approval" },
  throttle: { variant: "default", label: "Throttle" },
};

function DecisionBadge({ decision }: { decision: string }) {
  const d = DECISION_BADGE[decision.toLowerCase()];
  return (
    <Badge variant={d?.variant ?? "default"}>
      {d?.label ?? decision}
    </Badge>
  );
}

type Environment = "production" | "staging" | "dev";

const ENVIRONMENTS: { value: Environment; label: string }[] = [
  { value: "staging", label: "Staging" },
  { value: "production", label: "Production" },
  { value: "dev", label: "Development" },
];

// ---------------------------------------------------------------------------
// RunNowModal
// ---------------------------------------------------------------------------

export function RunNowModal({ workflowId, onClose }: RunNowModalProps) {
  const navigate = useNavigate();
  const { data: workflow, isLoading: loadingWorkflow } = useWorkflow(workflowId);
  const startRun = useStartRun();
  const dryRun = useDryRun();

  const inputSchema = (workflow?.metadata?.inputSchema ??
    workflow?.metadata?.input_schema) as Record<string, unknown> | undefined;
  const hasSchema =
    inputSchema &&
    typeof inputSchema === "object" &&
    Object.keys(inputSchema.properties ?? {}).length > 0;

  const [formValue, setFormValue] = useState<Record<string, unknown>>({});
  const [rawJson, setRawJson] = useState("{}");
  const [jsonError, setJsonError] = useState<string | null>(null);
  const [environment, setEnvironment] = useState<Environment>("staging");
  const [dryRunEnabled, setDryRunEnabled] = useState(false);
  const [dryRunResults, setDryRunResults] = useState<DryRunStepResult[] | null>(null);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // Close on Escape
  useEffect(() => {
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", handleKey);
    return () => window.removeEventListener("keydown", handleKey);
  }, [onClose]);

  const handleSubmit = useCallback(() => {
    setSubmitError(null);
    setJsonError(null);

    let input: Record<string, unknown>;
    if (hasSchema) {
      input = { ...formValue, _environment: environment };
    } else {
      try {
        const parsed = JSON.parse(rawJson);
        if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
          setJsonError("Input must be a JSON object");
          return;
        }
        input = { ...parsed, _environment: environment };
      } catch (err) {
        setJsonError(err instanceof Error ? err.message : "Invalid JSON");
        return;
      }
    }

    startRun.mutate(
      { workflowId, input },
      {
        onSuccess: (data) => {
          onClose();
          if (data?.run_id) {
            navigate(`/workflows/${workflowId}/runs/${data.run_id}`);
          }
        },
        onError: (err) => setSubmitError(err.message),
      },
    );
  }, [hasSchema, formValue, rawJson, environment, workflowId, startRun, onClose, navigate]);

  const handlePreview = useCallback(() => {
    setSubmitError(null);
    setJsonError(null);

    let input: Record<string, unknown>;
    if (hasSchema) {
      input = formValue;
    } else {
      try {
        const parsed = JSON.parse(rawJson);
        if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
          setJsonError("Input must be a JSON object");
          return;
        }
        input = parsed as Record<string, unknown>;
      } catch (err) {
        setJsonError(err instanceof Error ? err.message : "Invalid JSON");
        return;
      }
    }

    dryRun.mutate(
      { workflowId, input, environment: { name: environment } },
      {
        onSuccess: (data) => setDryRunResults(data?.steps ?? []),
        onError: (err) => setSubmitError(err.message),
      },
    );
  }, [hasSchema, formValue, rawJson, environment, workflowId, dryRun]);

  const deniedCount = dryRunResults?.filter(
    (s) => s.decision.toLowerCase() === "deny",
  ).length ?? 0;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="mx-4 w-full max-w-lg rounded-2xl bg-white shadow-xl">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border px-6 py-4">
          <h2 className="font-display text-lg font-semibold text-ink">
            Run {workflow ? truncate(workflow.name, 40) : "Workflow"}
          </h2>
          <button
            type="button"
            onClick={onClose}
            className="rounded-full p-1.5 hover:bg-surface2 transition"
          >
            <X className="h-4 w-4 text-muted" />
          </button>
        </div>

        {/* Body */}
        <div className="max-h-[60vh] overflow-y-auto px-6 py-4 space-y-5">
          {loadingWorkflow ? (
            <div className="flex items-center justify-center py-8 text-sm text-muted">
              <Loader className="mr-2 h-4 w-4 animate-spin" />
              Loading workflow...
            </div>
          ) : (
            <>
              {/* Input form */}
              {hasSchema ? (
                <div>
                  <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted">
                    Input Parameters
                  </h3>
                  <SchemaForm
                    schema={inputSchema}
                    value={formValue}
                    onChange={setFormValue}
                  />
                </div>
              ) : (
                <div>
                  <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted">
                    Input (JSON)
                  </h3>
                  <Textarea
                    value={rawJson}
                    onChange={(e) => {
                      setRawJson(e.target.value);
                      setJsonError(null);
                    }}
                    rows={6}
                    className="font-mono text-xs"
                    placeholder='{"key": "value"}'
                  />
                  {jsonError && (
                    <p className="mt-1 text-xs text-danger">{jsonError}</p>
                  )}
                </div>
              )}

              {/* Environment selector */}
              <div>
                <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-muted">
                  Environment
                </h3>
                <div className="flex gap-3">
                  {ENVIRONMENTS.map((env) => (
                    <label
                      key={env.value}
                      className={cn(
                        "flex cursor-pointer items-center gap-1.5 rounded-xl border px-3 py-2 text-xs font-medium transition",
                        environment === env.value
                          ? "border-accent bg-accent/10 text-accent"
                          : "border-border text-muted hover:border-accent/40",
                      )}
                    >
                      <input
                        type="radio"
                        name="environment"
                        value={env.value}
                        checked={environment === env.value}
                        onChange={() => setEnvironment(env.value)}
                        className="sr-only"
                      />
                      {env.label}
                    </label>
                  ))}
                </div>
              </div>

              {/* Dry-run toggle */}
              <label className="flex items-center gap-2 text-xs text-muted cursor-pointer">
                <input
                  type="checkbox"
                  checked={dryRunEnabled}
                  onChange={(e) => {
                    setDryRunEnabled(e.target.checked);
                    if (!e.target.checked) setDryRunResults(null);
                  }}
                  className="h-3.5 w-3.5"
                />
                Preview safety decisions before running
              </label>

              {/* Dry-run results */}
              {dryRunEnabled && dryRunResults && (
                <div className="space-y-3">
                  {deniedCount > 0 && (
                    <div className="flex items-center gap-2 rounded-xl border border-danger/30 bg-danger/5 px-4 py-2.5 text-xs font-medium text-danger">
                      <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
                      {deniedCount} step{deniedCount !== 1 ? "s" : ""} will be denied by policy &mdash; the run will fail at {deniedCount !== 1 ? "those steps" : "that step"}
                    </div>
                  )}

                  <div className="rounded-xl border border-border">
                    <div className="border-b border-border px-4 py-2 text-xs font-semibold uppercase tracking-wider text-muted">
                      Evaluation Results
                    </div>
                    <div className="divide-y divide-border">
                      {dryRunResults.map((step) => {
                        const decision = step.decision.toLowerCase();
                        return (
                          <div key={step.step_id} className="px-4 py-2.5 space-y-1">
                            <div className="flex items-center justify-between gap-2">
                              <span className="text-xs font-medium text-ink">
                                {step.step_id}
                              </span>
                              <DecisionBadge decision={step.decision} />
                            </div>
                            {decision === "deny" && step.reason && (
                              <p className="text-xs text-danger">
                                {truncate(step.reason, 200)}
                              </p>
                            )}
                            {decision === "require_approval" && (
                              <p className="text-xs text-warning">
                                This step will pause for approval
                                {step.reason ? ` \u2014 ${truncate(step.reason, 200)}` : ""}
                              </p>
                            )}
                          </div>
                        );
                      })}
                      {dryRunResults.length === 0 && (
                        <div className="px-4 py-3 text-xs text-muted">
                          No steps evaluated.
                        </div>
                      )}
                    </div>
                  </div>
                </div>
              )}
            </>
          )}

          {/* Error */}
          {submitError && (
            <p className="text-xs font-medium text-danger">{submitError}</p>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-2 border-t border-border px-6 py-4">
          <Button variant="outline" size="sm" type="button" onClick={onClose}>
            Cancel
          </Button>
          {dryRunEnabled && (
            <Button
              variant="outline"
              size="sm"
              type="button"
              onClick={handlePreview}
              disabled={dryRun.isPending || loadingWorkflow}
            >
              {dryRun.isPending ? (
                <Loader className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Eye className="h-3.5 w-3.5" />
              )}
              {dryRun.isPending ? "Previewing\u2026" : "Preview"}
            </Button>
          )}
          <Button
            size="sm"
            type="button"
            onClick={handleSubmit}
            disabled={startRun.isPending || loadingWorkflow}
          >
            {startRun.isPending ? (
              <Loader className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Play className="h-3.5 w-3.5" />
            )}
            {startRun.isPending ? "Starting\u2026" : "Start Run"}
          </Button>
        </div>
      </div>
    </div>
  );
}
