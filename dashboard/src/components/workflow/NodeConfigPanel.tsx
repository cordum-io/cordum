import { useEffect } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import type { Node } from "reactflow";
import { X, Trash2 } from "lucide-react";
import { Input } from "../ui/Input";
import { Select } from "../ui/Select";
import { Textarea } from "../ui/Textarea";
import { Button } from "../ui/Button";
import { AgentTaskPanel } from "./job/AgentTaskPanel";
import { PackActionPanel } from "./job/PackActionPanel";
import { ToolCallPanel } from "./job/ToolCallPanel";

// ---------------------------------------------------------------------------
// Per-type schemas
// ---------------------------------------------------------------------------

const jobSchema = z.object({
  label: z.string().min(1, "Name required"),
  topic: z.string().min(1, "Topic required"),
  capabilities: z.string().optional(),
  timeout: z.string().optional(),
  retryMax: z.coerce.number().int().min(0).optional(),
});

const approvalSchema = z.object({
  label: z.string().min(1, "Name required"),
  approverRoles: z.string().optional(),
  timeout: z.string().optional(),
});

const delaySchema = z.object({
  label: z.string().min(1, "Name required"),
  duration: z.string().min(1, "Duration required"),
});

const conditionSchema = z.object({
  label: z.string().min(1, "Name required"),
  expression: z.string().min(1, "Expression required"),
});

const notifySchema = z.object({
  label: z.string().min(1, "Name required"),
  channel: z.string().min(1, "Channel required"),
  messageTemplate: z.string().optional(),
});

const fanOutSchema = z.object({
  label: z.string().min(1, "Name required"),
  forEach: z.string().min(1, "For-each expression required").optional(),
  parallelism: z.coerce.number().int().min(1).optional(),
});

const httpSchema = z.object({
  label: z.string().min(1, "Name required"),
  method: z.string().min(1, "Method required"),
  url: z.string().min(1, "URL required").refine(
    (v) => !/^(javascript|data):/i.test(v),
    "Invalid URL scheme",
  ),
  headers: z.string().optional(),
  body: z.string().optional(),
  timeout: z.string().optional(),
});

const transformSchema = z.object({
  label: z.string().min(1, "Name required"),
  expression: z.string().min(1, "Expression required"),
  inputMapping: z.string().optional(),
  outputMapping: z.string().optional(),
});

const switchSchema = z.object({
  label: z.string().min(1, "Name required"),
  expression: z.string().optional(),
  cases: z.string().optional(),
  defaultBranch: z.string().optional(),
});

const loopSchema = z.object({
  label: z.string().min(1, "Name required"),
  forEach: z.string().min(1, "For-each expression required"),
  maxIterations: z.coerce.number().int().min(1).max(10_000).optional(),
  parallelism: z.coerce.number().int().min(1).optional(),
});

const subWorkflowSchema = z.object({
  label: z.string().min(1, "Name required"),
  workflowId: z.string().min(1, "Workflow ID required"),
  inputMapping: z.string().optional(),
});

const errorTriggerSchema = z.object({
  label: z.string().min(1, "Name required"),
  catchFrom: z.string().optional(),
  retryCount: z.coerce.number().int().min(0).optional(),
  retryDelay: z.string().optional(),
});

type AnySchema =
  | typeof jobSchema
  | typeof approvalSchema
  | typeof delaySchema
  | typeof conditionSchema
  | typeof notifySchema
  | typeof fanOutSchema
  | typeof httpSchema
  | typeof transformSchema
  | typeof switchSchema
  | typeof loopSchema
  | typeof subWorkflowSchema
  | typeof errorTriggerSchema;

function schemaForType(type: string): AnySchema {
  switch (type) {
    case "job": return jobSchema;
    case "approval": return approvalSchema;
    case "delay": return delaySchema;
    case "condition": return conditionSchema;
    case "notify": return notifySchema;
    case "fan-out": return fanOutSchema;
    case "http": return httpSchema;
    case "transform": return transformSchema;
    case "switch": return switchSchema;
    case "loop": return loopSchema;
    case "sub-workflow": return subWorkflowSchema;
    case "error-trigger": return errorTriggerSchema;
    default: return jobSchema;
  }
}

// ---------------------------------------------------------------------------
// Flatten node data -> form defaults
// ---------------------------------------------------------------------------

function nodeToDefaults(node: Node): Record<string, unknown> {
  const d = node.data ?? {};
  const config = (d.config ?? {}) as Record<string, unknown>;
  // Prefer direct backend fields from node.data, fall back to legacy config
  const caps = d.meta?.capability
    ? [d.meta.capability, ...(d.meta.requires ?? [])].filter(Boolean)
    : undefined;
  return {
    label: (d.label as string) ?? "",
    topic: d.topic ?? config.topic ?? "",
    capabilities: caps ? caps.join(", ") : (Array.isArray(config.capabilities) ? (config.capabilities as string[]).join(", ") : (config.capabilities ?? "")),
    timeout: d.timeout_sec ? `${d.timeout_sec}s` : (config.timeout ?? ""),
    retryMax: d.retry?.max_retries ?? config.retryMax ?? 0,
    approverRoles: Array.isArray(config.approverRoles) ? (config.approverRoles as string[]).join(", ") : (config.approverRoles ?? ""),
    duration: d.delay_sec ? `${d.delay_sec}s` : (d.delay_until ?? config.duration ?? ""),
    expression: d.condition ?? config.expression ?? "",
    channel: config.channel ?? "",
    messageTemplate: config.messageTemplate ?? "",
    parallelism: d.max_parallel ?? config.parallelism ?? 1,
    forEach: d.for_each ?? config.forEach ?? "",
    // http
    method: (d.input as Record<string, unknown>)?.method ?? config.method ?? "GET",
    url: (d.input as Record<string, unknown>)?.url ?? config.url ?? "",
    headers: config.headers ?? "",
    body: config.body ?? "",
    // transform
    inputMapping: config.inputMapping ?? "",
    outputMapping: config.outputMapping ?? "",
    // switch
    cases: config.cases ?? "",
    defaultBranch: config.defaultBranch ?? "",
    // loop
    maxIterations: config.maxIterations ?? 100,
    // sub-workflow
    workflowId: config.workflowId ?? "",
    // error-trigger
    catchFrom: config.catchFrom ?? "any",
    retryCount: config.retryCount ?? 0,
    retryDelay: config.retryDelay ?? "",
  };
}

// ---------------------------------------------------------------------------
// Flatten form values -> node data update
// ---------------------------------------------------------------------------

function formToNodeData(type: string, values: Record<string, unknown>) {
  const label = values.label as string;
  const config: Record<string, unknown> = {};
  // Direct backend fields to write alongside legacy config
  const direct: Record<string, unknown> = {};

  switch (type) {
    case "job":
      config.topic = values.topic;
      direct.topic = values.topic;
      if (values.capabilities) {
        config.capabilities = (values.capabilities as string).split(",").map((s) => s.trim()).filter(Boolean);
      }
      if (values.timeout) {
        config.timeout = values.timeout;
      }
      if (typeof values.retryMax === "number" && values.retryMax > 0) {
        config.retryMax = values.retryMax;
        direct.retry = { max_retries: values.retryMax };
      }
      break;
    case "approval":
      if (values.approverRoles) {
        config.approverRoles = (values.approverRoles as string).split(",").map((s) => s.trim()).filter(Boolean);
      }
      if (values.timeout) config.timeout = values.timeout;
      break;
    case "delay":
      config.duration = values.duration;
      break;
    case "condition":
      config.expression = values.expression;
      direct.condition = values.expression;
      break;
    case "notify":
      config.channel = values.channel;
      if (values.messageTemplate) config.messageTemplate = values.messageTemplate;
      break;
    case "fan-out":
      if (values.forEach) {
        config.forEach = values.forEach;
        direct.for_each = values.forEach;
      }
      if (typeof values.parallelism === "number") {
        config.parallelism = values.parallelism;
        direct.max_parallel = values.parallelism;
      }
      break;
    case "http":
      config.method = values.method;
      config.url = values.url;
      if (values.headers) config.headers = values.headers;
      if (values.body) config.body = values.body;
      if (values.timeout) config.timeout = values.timeout;
      break;
    case "transform":
      config.expression = values.expression;
      direct.condition = values.expression;
      if (values.inputMapping) config.inputMapping = values.inputMapping;
      if (values.outputMapping) config.outputMapping = values.outputMapping;
      break;
    case "switch":
      if (values.expression) config.expression = values.expression;
      if (values.cases) config.cases = values.cases;
      if (values.defaultBranch) config.defaultBranch = values.defaultBranch;
      break;
    case "loop":
      config.forEach = values.forEach;
      direct.for_each = values.forEach as string;
      if (typeof values.maxIterations === "number") config.maxIterations = values.maxIterations;
      if (typeof values.parallelism === "number") {
        config.parallelism = values.parallelism;
        direct.max_parallel = values.parallelism;
      }
      break;
    case "sub-workflow":
      config.workflowId = values.workflowId;
      if (values.inputMapping) config.inputMapping = values.inputMapping;
      break;
    case "error-trigger":
      if (values.catchFrom) config.catchFrom = values.catchFrom;
      if (typeof values.retryCount === "number" && values.retryCount > 0) config.retryCount = values.retryCount;
      if (values.retryDelay) config.retryDelay = values.retryDelay;
      break;
  }

  return { label, config, ...direct };
}

// ---------------------------------------------------------------------------
// Config panel
// ---------------------------------------------------------------------------

export interface NodeConfigPanelProps {
  node: Node;
  onSave: (nodeId: string, data: { label: string; config: Record<string, unknown> }) => void;
  onClose: () => void;
  onDelete?: (nodeId: string) => void;
}

export function NodeConfigPanel({ node, onSave, onClose, onDelete }: NodeConfigPanelProps) {
  const nodeType = node.type ?? "job";
  const isStartNode = node.id === "start" || node.type === "start";

  // Delegate to specialized panels for job node types
  if (nodeType === "agent-task" || nodeType === "job") {
    return <AgentTaskPanel node={node} onSave={onSave} onClose={onClose} onDelete={onDelete} />;
  }
  if (nodeType === "pack-action") {
    return <PackActionPanel node={node} onSave={onSave} onClose={onClose} onDelete={onDelete} />;
  }
  if (nodeType === "tool-call") {
    return <ToolCallPanel node={node} onSave={onSave} onClose={onClose} onDelete={onDelete} />;
  }

  const schema = schemaForType(nodeType);

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors, isDirty },
  } = useForm({
    resolver: zodResolver(schema),
    defaultValues: nodeToDefaults(node) as Record<string, string | number>,
  });

  // Reset form when selected node changes
  useEffect(() => {
    reset(nodeToDefaults(node) as Record<string, string | number>);
  }, [node.id, reset, node]);

  const onSubmit = (values: Record<string, unknown>) => {
    onSave(node.id, formToNodeData(nodeType, values));
  };

  return (
    <aside className="flex w-72 shrink-0 flex-col border-l border-border bg-surface1 overflow-y-auto">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        <h3 className="text-sm font-semibold text-ink capitalize">{nodeType} Config</h3>
        <button
          onClick={onClose}
          className="rounded-lg p-1 text-muted hover:bg-surface2 hover:text-ink transition-colors"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      {/* Form */}
      <form onSubmit={handleSubmit(onSubmit)} className="flex flex-1 flex-col gap-4 p-4">
        {/* Always: label */}
        <Field label="Name" error={errors.label?.message as string | undefined}>
          <Input {...register("label")} placeholder="Step name" />
        </Field>

        {/* Type-specific fields */}
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
              <Input {...register("expression")} placeholder="result.status" />
            </Field>
            <Field label="Cases" hint="JSON array">
              <Textarea {...register("cases")} placeholder='[{"value":"ok","label":"Success"},{"value":"err","label":"Error"}]' rows={4} />
            </Field>
            <Field label="Default Branch">
              <Input {...register("defaultBranch")} placeholder="step-id" />
            </Field>
          </>
        )}

        {nodeType === "loop" && (
          <>
            <Field label="For Each" error={errors.forEach?.message as string | undefined}>
              <Input {...register("forEach")} placeholder="result.items" />
            </Field>
            <Field label="Max Iterations" hint="safety cap, max 10000">
              <Input type="number" {...register("maxIterations")} />
            </Field>
            <Field label="Parallelism">
              <Input type="number" {...register("parallelism")} />
            </Field>
          </>
        )}

        {nodeType === "sub-workflow" && (
          <>
            <Field label="Workflow ID" error={errors.workflowId?.message as string | undefined}>
              <Input {...register("workflowId")} placeholder="workflow-abc123" />
            </Field>
            <Field label="Input Mapping" hint="JSON">
              <Textarea {...register("inputMapping")} placeholder='{"param": "$.steps.prev.output"}' rows={3} />
            </Field>
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
            <Button
              type="button"
              variant="danger"
              size="sm"
              className="w-full"
              onClick={() => onDelete(node.id)}
            >
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
// Tiny field wrapper
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
      <label className="mb-1 flex items-baseline gap-1 text-xs text-muted">
        {label}
        {hint && <span className="text-[10px] text-muted/60">({hint})</span>}
      </label>
      {children}
      {error && <p className="mt-0.5 text-[10px] text-danger">{error}</p>}
    </div>
  );
}
