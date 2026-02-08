import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import type { Node } from "reactflow";
import { X, ChevronDown, ChevronRight, Trash2 } from "lucide-react";
import { Input } from "../../ui/Input";
import { Textarea } from "../../ui/Textarea";
import { Select } from "../../ui/Select";
import { Button } from "../../ui/Button";
import { ComboboxInput } from "../../ui/ComboboxInput";
import { useTopics } from "../../../hooks/useSettings";
import { agentTaskSchema, type AgentTaskConfig } from "./schemas";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function nodeToDefaults(node: Node): AgentTaskConfig {
  const config = (node.data?.config ?? {}) as Record<string, unknown>;
  return {
    label: (node.data?.label as string) ?? "",
    topic: (config.topic as string) ?? "",
    prompt: (config.prompt as string) ?? "",
    adapterId: (config.adapterId as string) ?? "",
    priority: (config.priority as string) ?? "",
    maxInputTokens: config.maxInputTokens as number | undefined,
    maxOutputTokens: config.maxOutputTokens as number | undefined,
    maxTotalTokens: config.maxTotalTokens as number | undefined,
    deadlineMs: config.deadlineMs as number | undefined,
    allowSummarization: (config.allowSummarization as boolean) ?? false,
    allowRetrieval: (config.allowRetrieval as boolean) ?? false,
    memoryId: (config.memoryId as string) ?? "",
    contextMode: (config.contextMode as string) ?? "",
    timeout: (config.timeout as string) ?? "",
    retryMax: config.retryMax as number | undefined,
  };
}

function formToNodeData(values: AgentTaskConfig) {
  const { label, ...rest } = values;
  const config: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(rest)) {
    if (v !== undefined && v !== "" && v !== false) config[k] = v;
  }
  return { label, config };
}

function Field({ label, error, hint, children }: {
  label: string; error?: string; hint?: string; children: React.ReactNode;
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

// ---------------------------------------------------------------------------
// AgentTaskPanel
// ---------------------------------------------------------------------------

export interface AgentTaskPanelProps {
  node: Node;
  onSave: (nodeId: string, data: { label: string; config: Record<string, unknown> }) => void;
  onClose: () => void;
  onDelete?: (nodeId: string) => void;
}

export function AgentTaskPanel({ node, onSave, onClose, onDelete }: AgentTaskPanelProps) {
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const topicSuggestions = useTopics();

  const {
    register,
    handleSubmit,
    reset,
    watch,
    setValue,
    formState: { errors, isDirty },
  } = useForm<AgentTaskConfig>({
    resolver: zodResolver(agentTaskSchema),
    defaultValues: nodeToDefaults(node),
  });

  useEffect(() => {
    reset(nodeToDefaults(node));
  }, [node.id, reset, node]);

  const onSubmit = (values: AgentTaskConfig) => {
    onSave(node.id, formToNodeData(values));
  };

  return (
    <aside className="flex w-96 shrink-0 flex-col border-l border-border bg-surface1 overflow-y-auto">
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        <h3 className="text-sm font-semibold text-ink">Agent Task Config</h3>
        <button onClick={onClose} className="rounded-lg p-1 text-muted hover:bg-surface2 hover:text-ink transition-colors">
          <X className="h-4 w-4" />
        </button>
      </div>

      <form onSubmit={handleSubmit(onSubmit)} className="flex flex-1 flex-col gap-4 p-4">
        <Field label="Name" error={errors.label?.message}>
          <Input {...register("label")} placeholder="Step name" />
        </Field>

        <Field label="Topic" error={errors.topic?.message}>
          <ComboboxInput
            value={watch("topic") ?? ""}
            onChange={(v) => setValue("topic", v, { shouldDirty: true })}
            suggestions={topicSuggestions}
            placeholder="job.default"
          />
        </Field>

        <Field label="Prompt">
          <Textarea {...register("prompt")} placeholder="Describe what the agent should do..." rows={5} />
        </Field>

        <Field label="Adapter ID">
          <Input {...register("adapterId")} placeholder="default" />
        </Field>

        {/* Advanced section */}
        <button
          type="button"
          onClick={() => setAdvancedOpen(!advancedOpen)}
          className="flex items-center gap-1 text-xs font-medium text-muted hover:text-ink transition-colors"
        >
          {advancedOpen ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
          Advanced
        </button>

        {advancedOpen && (
          <div className="flex flex-col gap-3 pl-2 border-l-2 border-border/50">
            <Field label="Priority">
              <Select {...register("priority")}>
                <option value="">Default</option>
                <option value="low">Low</option>
                <option value="normal">Normal</option>
                <option value="high">High</option>
                <option value="critical">Critical</option>
              </Select>
            </Field>

            <Field label="Max Input Tokens">
              <Input type="number" {...register("maxInputTokens")} placeholder="0" />
            </Field>
            <Field label="Max Output Tokens">
              <Input type="number" {...register("maxOutputTokens")} placeholder="0" />
            </Field>
            <Field label="Max Total Tokens">
              <Input type="number" {...register("maxTotalTokens")} placeholder="0" />
            </Field>

            <Field label="Allow Summarization">
              <input type="checkbox" {...register("allowSummarization")} className="accent-accent" />
            </Field>
            <Field label="Allow Retrieval">
              <input type="checkbox" {...register("allowRetrieval")} className="accent-accent" />
            </Field>

            <Field label="Memory ID">
              <Input {...register("memoryId")} placeholder="optional" />
            </Field>
            <Field label="Context Mode">
              <Input {...register("contextMode")} placeholder="default" />
            </Field>
            <Field label="Deadline (ms)">
              <Input type="number" {...register("deadlineMs")} placeholder="0" />
            </Field>
            <Field label="Timeout">
              <Input {...register("timeout")} placeholder="30s" />
            </Field>
            <Field label="Max Retries">
              <Input type="number" {...register("retryMax")} />
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
