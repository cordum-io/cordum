import { useEffect } from "react";
import { useFieldArray, useForm, type Resolver } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import type { Node } from "reactflow";
import { X, Trash2 } from "lucide-react";
import { Input } from "../ui/Input";
import { Select } from "../ui/Select";
import { Textarea } from "../ui/Textarea";
import { Button } from "../ui/Button";
import { useWorkflows } from "../../hooks/useWorkflows";
import type { UnifiedNodeData } from "./types";
import {
  schemaForType,
  unifiedNodeToDefaults,
  formToUnifiedNodeData,
  type SwitchCaseFormValue,
} from "./configSchemas";
import { AgentTaskConfig } from "./config/AgentTaskConfig";
import { PackActionConfig } from "./config/PackActionConfig";
import { ToolCallConfig } from "./config/ToolCallConfig";

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface StudioConfigPanelProps {
  node: Node<UnifiedNodeData>;
  onSave: (nodeId: string, data: { label: string; config: Record<string, unknown> }) => void;
  onClose: () => void;
  onDelete?: (nodeId: string) => void;
  allNodes?: Node<UnifiedNodeData>[];
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export function StudioConfigPanel({ node, onSave, onClose, onDelete, allNodes }: StudioConfigPanelProps) {
  const nodeType = node.data.stepType ?? "job";
  const isStartNode = node.id === "start" || node.data.stepType === "start";

  // Delegate to specialized config panels for job node types
  if (nodeType === "agent-task" || nodeType === "job") {
    return <AgentTaskConfig node={node} onSave={onSave} onClose={onClose} onDelete={onDelete} />;
  }
  if (nodeType === "pack-action") {
    return <PackActionConfig node={node} onSave={onSave} onClose={onClose} onDelete={onDelete} />;
  }
  if (nodeType === "tool-call") {
    return <ToolCallConfig node={node} onSave={onSave} onClose={onClose} onDelete={onDelete} />;
  }

  return (
    <GenericConfigForm
      node={node}
      nodeType={nodeType}
      isStartNode={isStartNode}
      onSave={onSave}
      onClose={onClose}
      onDelete={onDelete}
      allNodes={allNodes}
    />
  );
}

// ---------------------------------------------------------------------------
// Generic config form (all non-job types)
// ---------------------------------------------------------------------------

function GenericConfigForm({
  node,
  nodeType,
  isStartNode,
  onSave,
  onClose,
  onDelete,
  allNodes,
}: {
  node: Node<UnifiedNodeData>;
  nodeType: string;
  isStartNode: boolean;
  onSave: StudioConfigPanelProps["onSave"];
  onClose: () => void;
  onDelete?: (nodeId: string) => void;
  allNodes?: Node<UnifiedNodeData>[];
}) {
  const { data: workflowOptions = [] } = useWorkflows();
  const schema = schemaForType(nodeType);

  const {
    register,
    handleSubmit,
    reset,
    watch,
    control,
    formState: { errors, isDirty },
  } = useForm({
    resolver: zodResolver(schema as z.ZodTypeAny) as Resolver<Record<string, unknown>>,
    defaultValues: unifiedNodeToDefaults(node.data) as Record<string, unknown>,
  });

  const { fields: switchCaseFields, append: appendSwitchCase, remove: removeSwitchCase } = useFieldArray({
    control,
    name: "switchCases" as never,
  });

  useEffect(() => {
    reset(unifiedNodeToDefaults(node.data) as Record<string, unknown>);
  }, [node.id, reset, node.data]);

  const onSubmit = (values: Record<string, unknown>) => {
    onSave(node.id, formToUnifiedNodeData(nodeType, values));
  };

  const selectedParallelSteps = watch("parallelSteps");
  const selectedStrategy = watch("completionStrategy");
  const selectedParallelCount = Array.isArray(selectedParallelSteps)
    ? selectedParallelSteps.length
    : typeof selectedParallelSteps === "string" && selectedParallelSteps.trim()
      ? 1
      : 0;
  const availableSteps = (allNodes ?? [])
    .filter((c) => c.id !== node.id && c.id !== "start" && c.data.stepType !== "start")
    .map((c) => ({ id: c.id, label: c.data.label ?? c.id }));

  return (
    <aside className="flex w-72 shrink-0 flex-col border-l border-border bg-surface1 overflow-y-auto">
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        <h3 className="text-sm font-semibold text-ink capitalize">{nodeType} Config</h3>
        <button
          type="button"
          onClick={onClose}
          className="rounded-xl p-1 text-muted-foreground hover:bg-surface2 hover:text-ink transition-colors"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      <form onSubmit={handleSubmit(onSubmit)} className="flex flex-1 flex-col gap-4 p-4">
        <Field label="Name" error={errors.label?.message as string | undefined}>
          <Input {...register("label")} placeholder="Step name" />
        </Field>

        {nodeType === "approval" && (
          <>
            <Field label="Approver Roles" hint="comma-separated">
              <Input {...register("approverRoles")} placeholder="admin, reviewer" />
            </Field>
            <Field label="Timeout">
              <Input {...register("timeout")} placeholder="1h" />
            </Field>
          </>
        )}

        {nodeType === "delay" && (
          <Field label="Duration" error={errors.duration?.message as string | undefined}>
            <Input {...register("duration")} placeholder="5m" />
          </Field>
        )}

        {nodeType === "condition" && (
          <Field label="Expression" error={errors.expression?.message as string | undefined}>
            <Textarea {...register("expression")} placeholder="result.status == 'ok'" rows={3} />
          </Field>
        )}

        {nodeType === "notify" && (
          <>
            <Field label="Channel" error={errors.channel?.message as string | undefined}>
              <Input {...register("channel")} placeholder="slack, email" />
            </Field>
            <Field label="Message Template">
              <Textarea {...register("messageTemplate")} placeholder="Job {{jobId}} completed" rows={3} />
            </Field>
          </>
        )}

        {nodeType === "fan-out" && (
          <>
            <Field label="For Each" hint="expression">
              <Input {...register("forEach")} placeholder="items" />
            </Field>
            <Field label="Parallelism">
              <Input type="number" {...register("parallelism")} />
            </Field>
          </>
        )}

        {nodeType === "parallel" && (
          <>
            <Field label="Child Steps" error={errors.parallelSteps?.message as string | undefined} hint="Ctrl/Cmd-click for multi-select">
              <select
                {...register("parallelSteps")}
                multiple
                size={Math.min(Math.max(4, availableSteps.length), 8)}
                className="w-full rounded-xl border border-border bg-surface1 px-2 py-1.5 text-xs text-ink outline-none focus:ring-2 focus:ring-accent"
              >
                {availableSteps.map((c) => (
                  <option key={c.id} value={c.id}>{c.label} ({c.id})</option>
                ))}
              </select>
            </Field>
            <Field label="Completion Strategy">
              <Select {...register("completionStrategy")}>
                <option value="all">all (all children must succeed)</option>
                <option value="any">any (first success wins)</option>
                <option value="n_of_m">n_of_m (threshold success)</option>
              </Select>
            </Field>
            {selectedStrategy === "n_of_m" && (
              <Field label="Required Successes" hint={`1-${Math.max(selectedParallelCount, 1)}`}>
                <Input type="number" {...register("requiredCount")} />
              </Field>
            )}
            <Field label="Max Concurrency" hint="optional throttle">
              <Input type="number" {...register("parallelism")} />
            </Field>
          </>
        )}

        {nodeType === "http" && (
          <>
            <Field label="Method" error={errors.method?.message as string | undefined}>
              <Select {...register("method")}>
                <option value="GET">GET</option>
                <option value="POST">POST</option>
                <option value="PUT">PUT</option>
                <option value="DELETE">DELETE</option>
              </Select>
            </Field>
            <Field label="URL" error={errors.url?.message as string | undefined}>
              <Input {...register("url")} placeholder="https://api.example.com/endpoint" />
            </Field>
            <Field label="Headers" hint="JSON">
              <Textarea {...register("headers")} placeholder='{"Content-Type":"application/json"}' rows={3} />
            </Field>
            <Field label="Body">
              <Textarea {...register("body")} placeholder="Request body template" rows={3} />
            </Field>
            <Field label="Timeout">
              <Input {...register("timeout")} placeholder="30s" />
            </Field>
          </>
        )}

        {nodeType === "transform" && (
          <>
            <Field label="Expression" error={errors.expression?.message as string | undefined}>
              <Textarea {...register("expression")} placeholder="result.data.map(item => item.name)" rows={4} />
            </Field>
            <Field label="Input Mapping">
              <Input {...register("inputMapping")} placeholder="$.steps.previous.output" />
            </Field>
            <Field label="Output Mapping">
              <Input {...register("outputMapping")} placeholder="$.result" />
            </Field>
          </>
        )}

        {nodeType === "switch" && (
          <>
            <Field label="Expression">
              <Textarea {...register("expression")} placeholder="input.route" rows={2} />
            </Field>
            <div className="space-y-2 rounded-xl border border-border p-3">
              <div className="flex items-center justify-between">
                <p className="text-xs font-semibold text-ink">Cases</p>
                <Button type="button" variant="ghost" size="sm" onClick={() => appendSwitchCase({ matchValue: "", stepId: "" } as SwitchCaseFormValue)}>
                  Add Case
                </Button>
              </div>
              {switchCaseFields.length === 0 && (
                <p className="text-xs text-muted-foreground">Add one or more match → target branch routes.</p>
              )}
              {switchCaseFields.map((field, index) => (
                <div key={field.id} className="grid grid-cols-[1fr_1fr_auto] gap-2">
                  <Input {...register(`switchCases.${index}.matchValue` as const)} placeholder="match value" />
                  <Select {...register(`switchCases.${index}.stepId` as const)}>
                    <option value="">Select target</option>
                    {availableSteps.map((c) => (
                      <option key={c.id} value={c.id}>{c.label} ({c.id})</option>
                    ))}
                  </Select>
                  <Button type="button" variant="ghost" size="sm" onClick={() => removeSwitchCase(index)}>
                    Remove
                  </Button>
                </div>
              ))}
            </div>
            <Field label="Default Branch">
              <Select {...register("defaultBranch")}>
                <option value="">None</option>
                {availableSteps.map((c) => (
                  <option key={c.id} value={c.id}>{c.label} ({c.id})</option>
                ))}
              </Select>
            </Field>
            <p className="text-xs text-muted-foreground">
              First matching case is selected. If none match, default branch is used.
            </p>
          </>
        )}

        {nodeType === "loop" && (
          <>
            <Field label="Body Step" error={errors.bodyStep?.message as string | undefined} hint="Step executed each iteration">
              <Select {...register("bodyStep")}>
                <option value="">Select body step</option>
                {availableSteps.map((c) => (
                  <option key={c.id} value={c.id}>{c.label} ({c.id})</option>
                ))}
              </Select>
            </Field>
            <Field label="Max Iterations" hint="safety cap, max 10000">
              <Input type="number" {...register("maxIterations")} />
            </Field>
            <Field label="Condition (while true)">
              <Textarea {...register("condition")} placeholder="loop.index < 5" rows={2} />
            </Field>
            <Field label="Until (stop when true)">
              <Textarea {...register("until")} placeholder="steps.scan.output.clean == true" rows={2} />
            </Field>
            <p className="text-xs text-muted-foreground">
              `condition` keeps iterating while truthy. `until` stops when truthy. If both are empty, the loop runs exactly max iterations.
            </p>
          </>
        )}

        {nodeType === "sub-workflow" && (
          <>
            <Field label="Workflow ID" error={errors.workflowId?.message as string | undefined}>
              <Select {...register("workflowId")}>
                <option value="">Select workflow</option>
                {workflowOptions.map((wf) => (
                  <option key={wf.id} value={wf.id}>{wf.name} ({wf.id})</option>
                ))}
              </Select>
            </Field>
            <Field label="Input Mapping" hint="JSON object of childInputKey -> parent expression">
              <Textarea {...register("subInputMapping")} placeholder='{"ticket_id": "${input.ticket_id}"}' rows={3} />
            </Field>
            <Field label="Output Mapping" hint="JSON object of parentOutputKey -> child expression">
              <Textarea {...register("subOutputMapping")} placeholder='{"result_ptr": "${child.steps.scan.result_ptr}"}' rows={3} />
            </Field>
            <Field label="Output Path" hint="optional run context destination">
              <Input {...register("outputPath")} placeholder="ctx.subworkflow.result" />
            </Field>
          </>
        )}

        {nodeType === "storage" && (
          <>
            <Field label="Operation" error={errors.operation?.message as string | undefined}>
              <Select {...register("operation")}>
                <option value="read">read</option>
                <option value="write">write</option>
                <option value="delete">delete</option>
              </Select>
            </Field>
            <Field label="Key Path" error={errors.key?.message as string | undefined} hint="dot-separated, e.g. data.user.name">
              <Input {...register("key")} placeholder="data.message" />
            </Field>
            {watch("operation") === "write" && (
              <Field label="Value" hint="expression or literal">
                <Textarea {...register("value")} placeholder="${input.name}" rows={2} />
              </Field>
            )}
            {watch("operation") === "read" && (
              <Field label="Output Path" hint="write result to run context">
                <Input {...register("outputPath")} placeholder="ctx.result" />
              </Field>
            )}
            <p className="text-xs text-muted-foreground">
              Storage steps read/write/delete values in the workflow run context using dot-separated key paths. Use `$&#123;expr&#125;` templates for dynamic values.
            </p>
          </>
        )}

        {nodeType === "error-trigger" && (
          <>
            <Field label="Catch From" hint="step IDs or 'any'">
              <Input {...register("catchFrom")} placeholder="any" />
            </Field>
            <Field label="Retry Count">
              <Input type="number" {...register("retryCount")} />
            </Field>
            <Field label="Retry Delay">
              <Input {...register("retryDelay")} placeholder="5s" />
            </Field>
          </>
        )}

        <div className="mt-auto space-y-2 pt-4">
          <Button type="submit" disabled={!isDirty} className="w-full">
            Save
          </Button>
          {onDelete && !isStartNode && (
            <Button type="button" variant="danger" size="sm" className="w-full" onClick={() => onDelete(node.id)}>
              <Trash2 className="h-3.5 w-3.5" />
              Delete Node
            </Button>
          )}
        </div>
      </form>
    </aside>
  );
}

// ---------------------------------------------------------------------------
// Field wrapper
// ---------------------------------------------------------------------------

function Field({
  label,
  error,
  hint,
  children,
}: {
  label: string;
  error?: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <label className="mb-1 flex items-baseline gap-1 text-xs text-muted-foreground">
        {label}
        {hint && <span className="text-xs text-muted/60">({hint})</span>}
      </label>
      {children}
      {error && <p className="mt-0.5 text-xs text-danger">{error}</p>}
    </div>
  );
}

