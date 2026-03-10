import { useEffect, useState, useCallback } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import type { Node } from "reactflow";
import { X, ChevronDown, ChevronRight, Plus, Trash2 } from "lucide-react";
import { Input } from "../../ui/Input";
import { Textarea } from "../../ui/Textarea";
import { Select } from "../../ui/Select";
import { Button } from "../../ui/Button";
import { toolCallSchema, type ToolCallConfig } from "./schemas";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function nodeToDefaults(node: Node): ToolCallConfig {
  const config = (node.data?.config ?? {}) as Record<string, unknown>;
  return {
    label: (node.data?.label as string) ?? "",
    capability: (config.capability as string) ?? "",
    prompt: (config.prompt as string) ?? "",
    riskTags: Array.isArray(config.riskTags) ? (config.riskTags as string[]) : undefined,
    topic: (config.topic as string) ?? "",
    priority: (config.priority as string) ?? "",
    timeout: (config.timeout as string) ?? "",
    retryMax: config.retryMax as number | undefined,
    labels: (config.labels as Record<string, string>) ?? undefined,
  };
}

function formToNodeData(values: ToolCallConfig) {
  const { label, ...rest } = values;
  const config: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(rest)) {
    if (v !== undefined && v !== "" && !(Array.isArray(v) && v.length === 0)) {
      if (typeof v === "object" && !Array.isArray(v) && Object.keys(v as object).length === 0) continue;
      config[k] = v;
    }
  }
  return { label, config };
}

function Field({ label, error, hint, children }: {
  label: string; error?: string; hint?: string; children: React.ReactNode;
}) {
  return (
    <div>
      <label className="mb-1 flex items-baseline gap-1 text-xs text-muted-foreground">
        {label}
        {hint && <span className="text-[10px] text-muted/60">({hint})</span>}
      </label>
      {children}
      {error && <p className="mt-0.5 text-[10px] text-danger">{error}</p>}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Inline TagInput
// ---------------------------------------------------------------------------

function TagInput({ value, onChange, placeholder }: {
  value: string[]; onChange: (tags: string[]) => void; placeholder?: string;
}) {
  const [text, setText] = useState(value.join(", "));

  useEffect(() => {
    setText(value.join(", "));
  }, [value]);

  return (
    <Input
      value={text}
      onChange={(e) => {
        setText(e.target.value);
        onChange(
          e.target.value.split(",").map((s) => s.trim()).filter(Boolean),
        );
      }}
      placeholder={placeholder}
    />
  );
}

// ---------------------------------------------------------------------------
// Inline KeyValueEditor
// ---------------------------------------------------------------------------

interface KVPair { key: string; value: string }

function KeyValueEditor({ value, onChange }: {
  value: Record<string, string>; onChange: (rec: Record<string, string>) => void;
}) {
  const pairs: KVPair[] = Object.entries(value).map(([k, v]) => ({ key: k, value: v }));

  const updatePairs = useCallback(
    (newPairs: KVPair[]) => {
      const rec: Record<string, string> = {};
      for (const p of newPairs) {
        if (p.key.trim()) rec[p.key.trim()] = p.value;
      }
      onChange(rec);
    },
    [onChange],
  );

  return (
    <div className="flex flex-col gap-1.5">
      {pairs.map((p, i) => (
        <div key={i} className="flex items-center gap-1">
          <Input
            value={p.key}
            onChange={(e) => {
              const next = [...pairs];
              next[i] = { ...next[i], key: e.target.value };
              updatePairs(next);
            }}
            placeholder="key"
            className="flex-1 !py-1.5 !px-2 text-xs"
          />
          <Input
            value={p.value}
            onChange={(e) => {
              const next = [...pairs];
              next[i] = { ...next[i], value: e.target.value };
              updatePairs(next);
            }}
            placeholder="value"
            className="flex-1 !py-1.5 !px-2 text-xs"
          />
          <button
            type="button"
            onClick={() => updatePairs(pairs.filter((_, j) => j !== i))}
            className="p-1 text-muted-foreground hover:text-danger"
          >
            <Trash2 className="h-3 w-3" />
          </button>
        </div>
      ))}
      <button
        type="button"
        onClick={() => updatePairs([...pairs, { key: "", value: "" }])}
        className="flex items-center gap-1 text-[10px] text-muted-foreground hover:text-ink"
      >
        <Plus className="h-3 w-3" /> Add label
      </button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// ToolCallPanel
// ---------------------------------------------------------------------------

export interface ToolCallPanelProps {
  node: Node;
  onSave: (nodeId: string, data: { label: string; config: Record<string, unknown> }) => void;
  onClose: () => void;
  onDelete?: (nodeId: string) => void;
}

export function ToolCallPanel({ node, onSave, onClose, onDelete }: ToolCallPanelProps) {
  const [advancedOpen, setAdvancedOpen] = useState(false);

  const {
    register,
    handleSubmit,
    reset,
    watch,
    setValue,
    formState: { errors, isDirty },
  } = useForm<ToolCallConfig>({
    resolver: zodResolver(toolCallSchema),
    defaultValues: nodeToDefaults(node),
  });

  useEffect(() => {
    reset(nodeToDefaults(node));
  }, [node.id, reset, node]);

  const onSubmit = (values: ToolCallConfig) => {
    onSave(node.id, formToNodeData(values));
  };

  return (
    <aside className="flex w-96 shrink-0 flex-col border-l border-border bg-surface1 overflow-y-auto">
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        <h3 className="text-sm font-semibold text-ink">Tool Call Config</h3>
        <button onClick={onClose} className="rounded-lg p-1 text-muted-foreground hover:bg-surface2 hover:text-ink transition-colors">
          <X className="h-4 w-4" />
        </button>
      </div>

      <form onSubmit={handleSubmit(onSubmit)} className="flex flex-1 flex-col gap-4 p-4">
        <Field label="Name" error={errors.label?.message}>
          <Input {...register("label")} placeholder="Step name" />
        </Field>

        <Field label="Capability" error={errors.capability?.message}>
          <Input {...register("capability")} placeholder="file.read" />
        </Field>

        <Field label="Prompt">
          <Textarea {...register("prompt")} placeholder="Describe what the tool should do..." rows={4} />
        </Field>

        <Field label="Risk Tags" hint="comma-separated">
          <TagInput
            value={watch("riskTags") ?? []}
            onChange={(tags) => setValue("riskTags", tags, { shouldDirty: true })}
            placeholder="network, filesystem"
          />
        </Field>

        {/* Advanced section */}
        <button
          type="button"
          onClick={() => setAdvancedOpen(!advancedOpen)}
          className="flex items-center gap-1 text-xs font-medium text-muted-foreground hover:text-ink transition-colors"
        >
          {advancedOpen ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
          Advanced
        </button>

        {advancedOpen && (
          <div className="flex flex-col gap-3 pl-2 border-l-2 border-border/50">
            <Field label="Topic">
              <Input {...register("topic")} placeholder="auto-derived" />
            </Field>
            <Field label="Priority">
              <Select {...register("priority")}>
                <option value="">Default</option>
                <option value="low">Low</option>
                <option value="normal">Normal</option>
                <option value="high">High</option>
                <option value="critical">Critical</option>
              </Select>
            </Field>
            <Field label="Timeout">
              <Input {...register("timeout")} placeholder="30s" />
            </Field>
            <Field label="Max Retries">
              <Input type="number" {...register("retryMax")} />
            </Field>
            <Field label="Labels" hint="key-value pairs">
              <KeyValueEditor
                value={watch("labels") ?? {}}
                onChange={(rec) => setValue("labels", rec, { shouldDirty: true })}
              />
            </Field>
          </div>
        )}

        <div className="mt-auto space-y-2 pt-4">
          <Button type="submit" disabled={!isDirty} className="w-full">Save</Button>
          {onDelete && node.id !== "start" && node.type !== "start" && (
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
