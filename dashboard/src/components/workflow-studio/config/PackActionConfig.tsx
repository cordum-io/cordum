import { useEffect, useState, useMemo } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import type { Node } from "reactflow";
import { X, ChevronDown, ChevronRight, Trash2 } from "lucide-react";
import { Input } from "../../ui/Input";
import { Textarea } from "../../ui/Textarea";
import { Select } from "../../ui/Select";
import { Button } from "../../ui/Button";
import { usePacks } from "../../../hooks/usePacks";
import { packActionSchema, type PackActionConfig as PackActionFormValues } from "../../workflow/job/schemas";
import type { UnifiedNodeData } from "../types";

function nodeToDefaults(d: UnifiedNodeData): PackActionFormValues {
  const config = (d.config ?? {}) as Record<string, unknown>;
  return {
    label: d.label ?? "",
    packId: (config.packId as string) ?? "",
    topic: d.topic ?? (config.topic as string) ?? "",
    capability: (config.capability as string) ?? "",
    input: (config.input as string) ?? "",
    riskTags: Array.isArray(config.riskTags) ? (config.riskTags as string[]) : undefined,
    requires: Array.isArray(config.requires) ? (config.requires as string[]) : undefined,
    timeout: d.timeout_sec ? `${d.timeout_sec}s` : (config.timeout as string) ?? "",
    retryMax: d.retry?.max_retries ?? (config.retryMax as number | undefined),
  };
}

function formToNodeData(values: PackActionFormValues) {
  const { label, ...rest } = values;
  const config: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(rest)) {
    if (v !== undefined && v !== "" && !(Array.isArray(v) && v.length === 0)) {
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
        {hint && <span className="text-xs text-muted/60">({hint})</span>}
      </label>
      {children}
      {error && <p className="mt-0.5 text-xs text-danger">{error}</p>}
    </div>
  );
}

function TagInput({ value, onChange, placeholder }: {
  value: string[]; onChange: (tags: string[]) => void; placeholder?: string;
}) {
  const [text, setText] = useState(value.join(", "));
  useEffect(() => { setText(value.join(", ")); }, [value]);
  return (
    <Input
      value={text}
      onChange={(e) => {
        setText(e.target.value);
        onChange(e.target.value.split(",").map((s) => s.trim()).filter(Boolean));
      }}
      placeholder={placeholder}
    />
  );
}

export interface PackActionConfigProps {
  node: Node<UnifiedNodeData>;
  onSave: (nodeId: string, data: { label: string; config: Record<string, unknown> }) => void;
  onClose: () => void;
  onDelete?: (nodeId: string) => void;
}

export function PackActionConfig({ node, onSave, onClose, onDelete }: PackActionConfigProps) {
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const { data: packsData } = usePacks();
  const packs = packsData?.items ?? [];
  const isStartNode = node.id === "start" || node.data.stepType === "start";

  const {
    register,
    handleSubmit,
    reset,
    watch,
    setValue,
    formState: { errors, isDirty },
  } = useForm<PackActionFormValues>({
    resolver: zodResolver(packActionSchema),
    defaultValues: nodeToDefaults(node.data),
  });

  useEffect(() => {
    reset(nodeToDefaults(node.data));
  }, [node.id, reset, node.data]);

  const selectedPackId = watch("packId");
  const selectedPack = useMemo(
    () => packs.find((p) => p.id === selectedPackId || p.name === selectedPackId),
    [packs, selectedPackId],
  );
  const capabilities = selectedPack?.capabilities ?? [];
  const hasCapabilities = capabilities.length > 0;
  const isMcpPack = selectedPack?.manifest
    ? String((selectedPack.manifest as Record<string, unknown>).type ?? "").toLowerCase() === "mcp"
    : false;

  useEffect(() => {
    if (selectedPack?.poolAssignment) {
      setValue("topic", `job.${selectedPack.poolAssignment}`, { shouldDirty: true });
    }
  }, [selectedPack, setValue]);

  const onSubmit = (values: PackActionFormValues) => {
    onSave(node.id, formToNodeData(values));
  };

  return (
    <aside className="flex w-96 shrink-0 flex-col border-l border-border bg-surface1 overflow-y-auto">
      <div className="flex items-center justify-between border-b border-border px-4 py-3">
        <h3 className="text-sm font-semibold text-ink">Pack Action Config</h3>
        <button type="button" onClick={onClose} className="rounded-lg p-1 text-muted-foreground hover:bg-surface2 hover:text-ink transition-colors">
          <X className="h-4 w-4" />
        </button>
      </div>

      <form onSubmit={handleSubmit(onSubmit)} className="flex flex-1 flex-col gap-4 p-4">
        <Field label="Name" error={errors.label?.message}>
          <Input {...register("label")} placeholder="Step name" />
        </Field>

        <Field label="Pack" error={errors.packId?.message}>
          <Select {...register("packId")}>
            <option value="">Select a pack...</option>
            {packs.map((p) => (
              <option key={p.id} value={p.id}>{p.name} ({p.version})</option>
            ))}
          </Select>
        </Field>

        <Field label="Action" hint={!hasCapabilities && selectedPackId ? "free-text" : undefined}>
          {hasCapabilities ? (
            <Select {...register("capability")}>
              <option value="">Select action...</option>
              {capabilities.map((cap) => (
                <option key={cap} value={cap}>{cap}</option>
              ))}
            </Select>
          ) : (
            <>
              <Input
                {...register("capability")}
                placeholder={selectedPackId ? "e.g. file.read, search, query" : "Select a pack first"}
                disabled={!selectedPackId}
              />
              {selectedPackId && !hasCapabilities && (
                <p className="mt-1 text-xs text-muted-foreground">
                  {isMcpPack
                    ? "MCP pack — type the tool/action name manually."
                    : "This pack has no declared actions. Enter an action name or leave empty."}
                </p>
              )}
            </>
          )}
        </Field>

        {selectedPackId && !hasCapabilities && (
          <Field label="Topic" hint="required for dispatch">
            <Input {...register("topic")} placeholder="job.pack-pool" />
          </Field>
        )}

        <Field label="Input" hint="JSON">
          <Textarea {...register("input")} placeholder='{"key": "value"}' rows={4} />
        </Field>

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
            <Field label="Topic" hint="auto-derived from pack">
              <Input {...register("topic")} placeholder="job.pack-pool" />
            </Field>
            <Field label="Risk Tags" hint="comma-separated">
              <TagInput
                value={watch("riskTags") ?? []}
                onChange={(tags) => setValue("riskTags", tags, { shouldDirty: true })}
                placeholder="network, filesystem"
              />
            </Field>
            <Field label="Requires" hint="comma-separated">
              <TagInput
                value={watch("requires") ?? []}
                onChange={(tags) => setValue("requires", tags, { shouldDirty: true })}
                placeholder="auth, db"
              />
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
