import { useEffect } from "react";
import { useFieldArray, useForm } from "react-hook-form";
import { logger } from "../../lib/logger";
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
import { useWorkflows } from "../../hooks/useWorkflows";

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

const parallelSchema = z.object({
  label: z.string().min(1, "Name required"),
  parallelSteps: z.preprocess(
    (value) => {
      if (Array.isArray(value)) return value;
      if (typeof value === "string" && value.trim()) return [value.trim()];
      return [];
    },
    z.array(z.string()).min(1, "Select at least one child step"),
  ),
  completionStrategy: z.enum(["all", "any", "n_of_m"]),
  requiredCount: z.coerce.number().int().min(1).optional(),
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
  switchCases: z
    .array(
      z.object({
        matchValue: z.string().optional(),
        stepId: z.string().optional(),
      }),
    )
    .optional(),
  defaultBranch: z.string().optional(),
});

const loopSchema = z.object({
  label: z.string().min(1, "Name required"),
  bodyStep: z.string().min(1, "Body step required"),
  maxIterations: z.coerce.number().int().min(1).max(10_000).optional(),
  condition: z.string().optional(),
  until: z.string().optional(),
});

const subWorkflowSchema = z.object({
  label: z.string().min(1, "Name required"),
  workflowId: z.string().min(1, "Workflow ID required"),
  subInputMapping: z.string().optional(),
  subOutputMapping: z.string().optional(),
  outputPath: z.string().optional(),
});

const storageSchema = z.object({
  label: z.string().min(1, "Name required"),
  operation: z.enum(["read", "write", "delete"]),
  key: z.string().min(1, "Key path required"),
  value: z.string().optional(),
  outputPath: z.string().optional(),
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
  | typeof parallelSchema
  | typeof httpSchema
  | typeof transformSchema
  | typeof switchSchema
  | typeof loopSchema
  | typeof subWorkflowSchema
  | typeof storageSchema
  | typeof errorTriggerSchema;

function schemaForType(type: string): AnySchema {
  switch (type) {
    case "job": return jobSchema;
    case "approval": return approvalSchema;
    case "delay": return delaySchema;
    case "condition": return conditionSchema;
    case "notify": return notifySchema;
    case "fan-out": return fanOutSchema;
    case "parallel": return parallelSchema;
    case "http": return httpSchema;
    case "transform": return transformSchema;
    case "switch": return switchSchema;
    case "loop": return loopSchema;
    case "sub-workflow": return subWorkflowSchema;
    case "storage": return storageSchema;
    case "error-trigger": return errorTriggerSchema;
    default: return jobSchema;
  }
}

function mappingToEditorValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (value && typeof value === "object" && !Array.isArray(value)) {
    try {
      return JSON.stringify(value, null, 2);
    } catch {
      logger.debug("node-config", "JSON.stringify failed in mappingToEditorValue");
      return "";
    }
  }
  return "";
}

function parseMappingEditorValue(value: unknown): unknown {
  if (typeof value !== "string") return undefined;
  const trimmed = value.trim();
  if (!trimmed) return undefined;
  try {
    const parsed = JSON.parse(trimmed);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
  } catch {
    logger.debug("node-config", "JSON parse failed in parseMappingEditorValue, treating as string");
    return trimmed;
  }
  return trimmed;
}

// ---------------------------------------------------------------------------
// Flatten node data -> form defaults
// ---------------------------------------------------------------------------

function nodeToDefaults(node: Node): Record<string, unknown> {
  const d = node.data ?? {};
  const config = (d.config ?? {}) as Record<string, unknown>;
  const input = (d.input as Record<string, unknown> | undefined) ?? {};
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
    parallelSteps: Array.isArray((d.input as Record<string, unknown> | undefined)?.steps)
      ? ((d.input as Record<string, unknown>).steps as unknown[]).map((v) => String(v).trim()).filter(Boolean)
      : Array.isArray(config.parallelSteps)
        ? (config.parallelSteps as unknown[]).map((v) => String(v).trim()).filter(Boolean)
        : [],
    completionStrategy:
      (typeof (d.input as Record<string, unknown> | undefined)?.strategy === "string"
        ? ((d.input as Record<string, unknown>).strategy as string)
        : typeof config.completionStrategy === "string"
          ? (config.completionStrategy as string)
          : "all") ?? "all",
    requiredCount:
      (typeof (d.input as Record<string, unknown> | undefined)?.required === "number"
        ? ((d.input as Record<string, unknown>).required as number)
        : typeof config.requiredCount === "number"
          ? (config.requiredCount as number)
          : 1) ?? 1,
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
    switchCases: parseSwitchCasesForEditor(input.cases ?? config.switchCases ?? config.cases),
    defaultBranch:
      (typeof input.default === "string" && input.default.trim()
        ? input.default
        : typeof input.default_step === "string" && input.default_step.trim()
          ? input.default_step
          : typeof config.defaultBranch === "string"
            ? config.defaultBranch
            : "") ?? "",
    // loop
    bodyStep:
      (typeof input.body_step === "string" && input.body_step.trim()
        ? input.body_step
        : typeof input.body === "string" && input.body.trim()
          ? input.body
          : typeof config.bodyStep === "string"
            ? config.bodyStep
            : "") ?? "",
    maxIterations:
      (typeof input.max_iterations === "number"
        ? input.max_iterations
        : typeof input.maxIterations === "number"
          ? input.maxIterations
          : config.maxIterations) ?? 100,
    condition:
      (typeof input.condition === "string"
        ? input.condition
        : typeof input.while === "string"
          ? input.while
          : config.condition) ?? "",
    until: (typeof input.until === "string" ? input.until : config.until) ?? "",
    // sub-workflow
    workflowId:
      (typeof input.workflow_id === "string" && input.workflow_id.trim()
        ? input.workflow_id
        : typeof config.workflowId === "string"
          ? config.workflowId
          : "") ?? "",
    subInputMapping: mappingToEditorValue(input.input_mapping ?? config.inputMapping),
    subOutputMapping: mappingToEditorValue(input.output_mapping ?? config.outputMapping),
    outputPath: d.output_path ?? config.outputPath ?? "",
    // storage
    operation: input.operation ?? config.operation ?? "read",
    key: input.key ?? config.key ?? "",
    value: input.value != null ? String(input.value) : (config.value ?? ""),
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
    case "parallel": {
      const selectedSteps = Array.isArray(values.parallelSteps)
        ? values.parallelSteps.map((v) => String(v).trim()).filter(Boolean)
        : typeof values.parallelSteps === "string"
          ? values.parallelSteps
              .split(",")
              .map((v) => v.trim())
              .filter(Boolean)
          : [];
      const strategy = typeof values.completionStrategy === "string" ? values.completionStrategy : "all";
      const requiredCount =
        typeof values.requiredCount === "number"
          ? Math.floor(values.requiredCount)
          : typeof values.requiredCount === "string"
            ? Number.parseInt(values.requiredCount, 10)
            : undefined;
      config.parallelSteps = selectedSteps;
      config.completionStrategy = strategy;
      if (strategy === "n_of_m" && typeof requiredCount === "number" && requiredCount > 0) {
        config.requiredCount = requiredCount;
      }
      if (typeof values.parallelism === "number") {
        config.parallelism = values.parallelism;
        direct.max_parallel = values.parallelism;
      }
      const input: Record<string, unknown> = { steps: selectedSteps, strategy };
      if (strategy === "n_of_m" && typeof requiredCount === "number" && requiredCount > 0) {
        input.required = requiredCount;
      }
      direct.input = input;
      break;
    }
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
      if (typeof values.expression === "string" && values.expression.trim()) {
        config.expression = values.expression.trim();
        direct.condition = values.expression.trim();
      }
      if (typeof values.defaultBranch === "string" && values.defaultBranch.trim()) {
        config.defaultBranch = values.defaultBranch.trim();
      }
      if (Array.isArray(values.switchCases)) {
        const normalized = values.switchCases
          .map((entry) => {
            if (!entry || typeof entry !== "object") return null;
            const raw = entry as Record<string, unknown>;
            const stepId =
              typeof raw.stepId === "string" && raw.stepId.trim()
                ? raw.stepId.trim()
                : typeof raw.step === "string" && raw.step.trim()
                  ? raw.step.trim()
                  : "";
            if (!stepId) return null;
            const matchValue =
              typeof raw.matchValue === "string"
                ? raw.matchValue
                : typeof raw.match === "string"
                  ? raw.match
                  : typeof raw.when === "string"
                    ? raw.when
                    : raw.matchValue == null
                      ? ""
                      : String(raw.matchValue);
            return { match: matchValue, next: stepId };
          })
          .filter((entry): entry is { match: string; next: string } => entry !== null);
        const branches: Record<string, string> = {};
        for (let idx = 0; idx < normalized.length; idx++) {
          const entry = normalized[idx];
          const key = entry.match.trim() || `case_${idx + 1}`;
          branches[key] = entry.next;
        }
        if (typeof values.defaultBranch === "string" && values.defaultBranch.trim()) {
          branches.default = values.defaultBranch.trim();
        }
        config.switchCases = values.switchCases;
        config.cases = normalized;
        if (Object.keys(branches).length > 0) {
          config.branches = branches;
        }
        direct.input = {
          cases: normalized,
          ...(typeof values.defaultBranch === "string" && values.defaultBranch.trim()
            ? { default: values.defaultBranch.trim() }
            : {}),
        };
      } else if (typeof values.defaultBranch === "string" && values.defaultBranch.trim()) {
        direct.input = { default: values.defaultBranch.trim() };
      }
      break;
    case "loop":
      if (typeof values.bodyStep === "string" && values.bodyStep.trim()) {
        config.bodyStep = values.bodyStep.trim();
      }
      if (typeof values.maxIterations === "number") {
        config.maxIterations = values.maxIterations;
      }
      if (typeof values.condition === "string" && values.condition.trim()) {
        config.condition = values.condition.trim();
      }
      if (typeof values.until === "string" && values.until.trim()) {
        config.until = values.until.trim();
      }
      direct.input = {
        body_step: typeof values.bodyStep === "string" ? values.bodyStep.trim() : "",
        max_iterations: typeof values.maxIterations === "number" ? values.maxIterations : 100,
        ...(typeof values.condition === "string" && values.condition.trim()
          ? { condition: values.condition.trim() }
          : {}),
        ...(typeof values.until === "string" && values.until.trim()
          ? { until: values.until.trim() }
          : {}),
      };
      break;
    case "sub-workflow":
      config.workflowId = values.workflowId;
      if (typeof values.outputPath === "string" && values.outputPath.trim()) {
        config.outputPath = values.outputPath.trim();
        direct.output_path = values.outputPath.trim();
      }
      const inputMapping = parseMappingEditorValue(values.subInputMapping);
      const outputMapping = parseMappingEditorValue(values.subOutputMapping);
      if (inputMapping !== undefined) config.inputMapping = inputMapping;
      if (outputMapping !== undefined) config.outputMapping = outputMapping;
      direct.input = {
        workflow_id: values.workflowId,
        ...(inputMapping !== undefined ? { input_mapping: inputMapping } : {}),
        ...(outputMapping !== undefined ? { output_mapping: outputMapping } : {}),
      };
      break;
    case "storage": {
      const op = typeof values.operation === "string" ? values.operation : "read";
      const storageKey = typeof values.key === "string" ? values.key.trim() : "";
      config.operation = op;
      config.key = storageKey;
      const storageInput: Record<string, unknown> = { operation: op, key: storageKey };
      if (op === "write" && typeof values.value === "string" && values.value.trim()) {
        storageInput.value = values.value.trim();
        config.value = values.value.trim();
      }
      direct.input = storageInput;
      if (typeof values.outputPath === "string" && values.outputPath.trim()) {
        config.outputPath = values.outputPath.trim();
        direct.output_path = values.outputPath.trim();
      }
      break;
    }
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
  allNodes?: Node[];
}

type SwitchCaseFormValue = {
  matchValue: string;
  stepId: string;
};

function parseSwitchCasesForEditor(value: unknown): SwitchCaseFormValue[] {
  const parseObjectEntry = (entry: Record<string, unknown>): SwitchCaseFormValue | null => {
    const matchRaw = entry.match ?? entry.when ?? entry.value;
    const stepRaw = entry.next ?? entry.step ?? entry.target ?? entry.step_id;
    const stepId = typeof stepRaw === "string" ? stepRaw.trim() : "";
    if (!stepId) return null;
    return {
      matchValue: matchRaw == null ? "" : String(matchRaw),
      stepId,
    };
  };

  const parseArray = (items: unknown[]): SwitchCaseFormValue[] =>
    items
      .map((item) => {
        if (!item || typeof item !== "object") return null;
        return parseObjectEntry(item as Record<string, unknown>);
      })
      .filter((item): item is SwitchCaseFormValue => item !== null);

  if (Array.isArray(value)) {
    return parseArray(value);
  }

  if (value && typeof value === "object") {
    return Object.entries(value as Record<string, unknown>)
      .map(([matchValue, stepRaw]) => {
        const stepId = typeof stepRaw === "string" ? stepRaw.trim() : "";
        if (!stepId) return null;
        return { matchValue, stepId };
      })
      .filter((item): item is SwitchCaseFormValue => item !== null);
  }

  if (typeof value === "string" && value.trim()) {
    try {
      const parsed = JSON.parse(value);
      return parseSwitchCasesForEditor(parsed);
    } catch {
      logger.debug("node-config", "JSON parse failed for switch cases");
      return [];
    }
  }

  return [];
}

export function NodeConfigPanel({ node, onSave, onClose, onDelete, allNodes }: NodeConfigPanelProps) {
  const nodeType = node.type ?? "job";
  const isStartNode = node.id === "start" || node.type === "start";
  const { data: workflowOptions = [] } = useWorkflows();

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
    watch,
    control,
    formState: { errors, isDirty },
  } = useForm({
    resolver: zodResolver(schema),
    defaultValues: nodeToDefaults(node) as Record<string, unknown>,
  });

  const { fields: switchCaseFields, append: appendSwitchCase, remove: removeSwitchCase } = useFieldArray({
    control,
    name: "switchCases" as never,
  });

  // Reset form when selected node changes
  useEffect(() => {
    reset(nodeToDefaults(node) as Record<string, unknown>);
  }, [node.id, reset, node]);

  const onSubmit = (values: Record<string, unknown>) => {
    onSave(node.id, formToNodeData(nodeType, values));
  };

  const selectedParallelSteps = watch("parallelSteps");
  const selectedStrategy = watch("completionStrategy");
  const selectedParallelCount = Array.isArray(selectedParallelSteps)
    ? selectedParallelSteps.length
    : typeof selectedParallelSteps === "string" && selectedParallelSteps.trim()
      ? 1
      : 0;
  const availableParallelSteps = (allNodes ?? [])
    .filter((candidate) => candidate.id !== node.id && candidate.id !== "start" && candidate.type !== "start")
    .map((candidate) => ({
      id: candidate.id,
      label: (candidate.data?.label as string) ?? candidate.id,
    }));

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

        {nodeType === "parallel" && (
          <>
            <Field label="Child Steps" error={errors.parallelSteps?.message as string | undefined} hint="Ctrl/Cmd-click for multi-select">
              <select
                {...register("parallelSteps")}
                multiple
                size={Math.min(Math.max(4, availableParallelSteps.length), 8)}
                className="w-full rounded-lg border border-border bg-surface1 px-2 py-1.5 text-xs text-ink outline-none focus:ring-2 focus:ring-accent"
              >
                {availableParallelSteps.map((candidate) => (
                  <option key={candidate.id} value={candidate.id}>
                    {candidate.label} ({candidate.id})
                  </option>
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
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => appendSwitchCase({ matchValue: "", stepId: "" })}
                >
                  Add Case
                </Button>
              </div>
              {switchCaseFields.length === 0 && (
                <p className="text-[11px] text-muted">Add one or more match → target branch routes.</p>
              )}
              {switchCaseFields.map((field, index) => (
                <div key={field.id} className="grid grid-cols-[1fr_1fr_auto] gap-2">
                  <Input
                    {...register(`switchCases.${index}.matchValue` as const)}
                    placeholder="match value"
                  />
                  <Select {...register(`switchCases.${index}.stepId` as const)}>
                    <option value="">Select target</option>
                    {availableParallelSteps.map((candidate) => (
                      <option key={candidate.id} value={candidate.id}>
                        {candidate.label} ({candidate.id})
                      </option>
                    ))}
                  </Select>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => removeSwitchCase(index)}
                  >
                    Remove
                  </Button>
                </div>
              ))}
            </div>
            <Field label="Default Branch">
              <Select {...register("defaultBranch")}>
                <option value="">None</option>
                {availableParallelSteps.map((candidate) => (
                  <option key={candidate.id} value={candidate.id}>
                    {candidate.label} ({candidate.id})
                  </option>
                ))}
              </Select>
            </Field>
            <p className="text-[11px] text-muted">
              First matching case is selected. If none match, default branch is used.
            </p>
          </>
        )}

        {nodeType === "loop" && (
          <>
            <Field label="Body Step" error={errors.bodyStep?.message as string | undefined} hint="Step executed each iteration">
              <Select {...register("bodyStep")}>
                <option value="">Select body step</option>
                {availableParallelSteps.map((candidate) => (
                  <option key={candidate.id} value={candidate.id}>
                    {candidate.label} ({candidate.id})
                  </option>
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
            <p className="text-[11px] text-muted">
              `condition` keeps iterating while truthy. `until` stops when truthy. If both are empty, the loop runs exactly max iterations.
            </p>
          </>
        )}

        {nodeType === "sub-workflow" && (
          <>
            <Field label="Workflow ID" error={errors.workflowId?.message as string | undefined}>
              <Select {...register("workflowId")}>
                <option value="">Select workflow</option>
                {workflowOptions.map((workflow) => (
                  <option key={workflow.id} value={workflow.id}>
                    {workflow.name} ({workflow.id})
                  </option>
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
            <p className="text-[11px] text-muted">
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
