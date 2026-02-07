import { useEffect } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import type { Node } from "reactflow";
import { X } from "lucide-react";
import { Input } from "../ui/Input";
import { Textarea } from "../ui/Textarea";
import { Button } from "../ui/Button";

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

type AnySchema =
  | typeof jobSchema
  | typeof approvalSchema
  | typeof delaySchema
  | typeof conditionSchema
  | typeof notifySchema
  | typeof fanOutSchema;

function schemaForType(type: string): AnySchema {
  switch (type) {
    case "job": return jobSchema;
    case "approval": return approvalSchema;
    case "delay": return delaySchema;
    case "condition": return conditionSchema;
    case "notify": return notifySchema;
    case "fan-out": return fanOutSchema;
    default: return jobSchema;
  }
}

// ---------------------------------------------------------------------------
// Flatten node data -> form defaults
// ---------------------------------------------------------------------------

function nodeToDefaults(node: Node): Record<string, unknown> {
  const config = (node.data?.config ?? {}) as Record<string, unknown>;
  return {
    label: (node.data?.label as string) ?? "",
    topic: config.topic ?? "",
    capabilities: Array.isArray(config.capabilities) ? (config.capabilities as string[]).join(", ") : (config.capabilities ?? ""),
    timeout: config.timeout ?? "",
    retryMax: config.retryMax ?? 0,
    approverRoles: Array.isArray(config.approverRoles) ? (config.approverRoles as string[]).join(", ") : (config.approverRoles ?? ""),
    duration: config.duration ?? "",
    expression: config.expression ?? "",
    channel: config.channel ?? "",
    messageTemplate: config.messageTemplate ?? "",
    parallelism: config.parallelism ?? 1,
    forEach: config.forEach ?? "",
  };
}

// ---------------------------------------------------------------------------
// Flatten form values -> node data update
// ---------------------------------------------------------------------------

function formToNodeData(type: string, values: Record<string, unknown>) {
  const label = values.label as string;
  const config: Record<string, unknown> = {};

  switch (type) {
    case "job":
      config.topic = values.topic;
      if (values.capabilities) {
        config.capabilities = (values.capabilities as string).split(",").map((s) => s.trim()).filter(Boolean);
      }
      if (values.timeout) config.timeout = values.timeout;
      if (typeof values.retryMax === "number" && values.retryMax > 0) config.retryMax = values.retryMax;
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
      break;
    case "notify":
      config.channel = values.channel;
      if (values.messageTemplate) config.messageTemplate = values.messageTemplate;
      break;
    case "fan-out":
      if (values.forEach) config.forEach = values.forEach;
      if (typeof values.parallelism === "number") config.parallelism = values.parallelism;
      break;
  }

  return { label, config };
}

// ---------------------------------------------------------------------------
// Config panel
// ---------------------------------------------------------------------------

export interface NodeConfigPanelProps {
  node: Node;
  onSave: (nodeId: string, data: { label: string; config: Record<string, unknown> }) => void;
  onClose: () => void;
}

export function NodeConfigPanel({ node, onSave, onClose }: NodeConfigPanelProps) {
  const nodeType = node.type ?? "job";
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
        {nodeType === "job" && (
          <>
            <Field label="Topic" error={errors.topic?.message as string | undefined}>
              <Input {...register("topic")} placeholder="job.default" />
            </Field>
            <Field label="Capabilities" hint="comma-separated">
              <Input {...register("capabilities")} placeholder="read, write" />
            </Field>
            <Field label="Timeout">
              <Input {...register("timeout")} placeholder="30s" />
            </Field>
            <Field label="Max Retries">
              <Input type="number" {...register("retryMax")} />
            </Field>
          </>
        )}

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

        <div className="mt-auto pt-4">
          <Button type="submit" disabled={!isDirty} className="w-full">
            Save
          </Button>
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
