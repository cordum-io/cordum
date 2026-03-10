import { useCallback, useState } from "react";
import type { ReactNode } from "react";
import { useForm, Controller, type UseFormSetError } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { ChevronDown, ChevronRight, Loader, RefreshCw, X } from "lucide-react";
import { ApiError } from "../../api/client";
import type { SubmitJobInput, SubmitJobResponse } from "../../api/types";
import { useSubmitJob } from "../../hooks/useJobs";
import { useTopics } from "../../hooks/useSettings";
import { Drawer } from "../ui/Drawer";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import { Select } from "../ui/Select";
import { Textarea } from "../ui/Textarea";
import { TagInput } from "../ui/TagInput";
import { KeyValueEditor } from "../ui/KeyValueEditor";
import { ComboboxInput } from "../ui/ComboboxInput";

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

const topicRegex = /^[a-zA-Z0-9._-]+$/;

const submitJobSchema = z.object({
  topic: z
    .string()
    .trim()
    .min(1, "Topic is required")
    .max(256, "Topic must be 256 characters or less")
    .regex(topicRegex, "Topic must use letters, numbers, dots, dashes, or underscores"),
  prompt: z.string().trim().min(1, "Prompt is required"),
  priority: z.enum(["low", "normal", "high", "critical"]),
  capabilities: z.array(z.string().trim().min(1)).default([]),
  risk_tags: z.array(z.string().trim().min(1)).default([]),
  labels: z.array(
    z.object({
      key: z.string().trim().max(64, "Label key must be 64 characters or less"),
      value: z.string().trim().max(256, "Label value must be 256 characters or less"),
    }),
  ).default([]),
  adapter_id: z.string().trim().max(200, "Adapter ID is too long"),
  memory_id: z.string().trim().max(200, "Memory ID is too long"),
  budget: z
    .string()
    .trim()
    .refine((v) => v === "" || /^\d+$/.test(v), "Budget must be a non-negative integer"),
  context_hints: z.array(z.string().trim().min(1)).default([]),
  workflow_id: z.string().trim().max(200, "Workflow ID is too long"),
  pack_id: z.string().trim().max(200, "Pack ID is too long"),
  idempotency_key: z.string().trim().max(200, "Idempotency key is too long"),
});

type FormValues = z.infer<typeof submitJobSchema>;

const DEFAULT_VALUES: FormValues = {
  topic: "",
  prompt: "",
  priority: "normal",
  capabilities: [],
  risk_tags: [],
  labels: [],
  adapter_id: "",
  memory_id: "",
  budget: "",
  context_hints: [],
  workflow_id: "",
  pack_id: "",
  idempotency_key: "",
};

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function cleanOptional(value: string): string | undefined {
  const trimmed = value.trim();
  return trimmed ? trimmed : undefined;
}

function dedupe(values: string[]): string[] {
  return Array.from(
    new Set(
      values
        .map((v) => v.trim())
        .filter(Boolean),
    ),
  );
}

function apiMessage(err: ApiError): string {
  if (err.body && typeof err.body === "object") {
    const obj = err.body as Record<string, unknown>;
    if (typeof obj.reason === "string" && obj.reason.trim()) return obj.reason.trim();
    if (typeof obj.error === "string" && obj.error.trim()) return obj.error.trim();
    if (typeof obj.message === "string" && obj.message.trim()) return obj.message.trim();
  }
  return err.message;
}

function handleSubmitError(
  error: unknown,
  setError: UseFormSetError<FormValues>,
  setSubmitError: (msg: string) => void,
) {
  if (!(error instanceof ApiError)) {
    setSubmitError(error instanceof Error ? error.message : "Failed to submit job");
    return;
  }

  const msg = apiMessage(error);
  if (error.status === 429) {
    setSubmitError("System at capacity. Please retry in a moment.");
    return;
  }
  if (error.status === 403) {
    setSubmitError(msg || "Job submission denied by policy.");
    return;
  }
  if (error.status === 400) {
    const m = msg.toLowerCase();
    if (m.includes("topic")) {
      setError("topic", { type: "server", message: msg });
      return;
    }
    if (m.includes("prompt")) {
      setError("prompt", { type: "server", message: msg });
      return;
    }
    if (m.includes("memory")) {
      setError("memory_id", { type: "server", message: msg });
      return;
    }
    if (m.includes("budget") || m.includes("token") || m.includes("deadline")) {
      setError("budget", { type: "server", message: msg });
      return;
    }
    setSubmitError(msg || "Validation failed");
    return;
  }

  setSubmitError(msg || "Job submission failed");
}

function Field({
  label,
  hint,
  error,
  children,
}: {
  label: string;
  hint?: string;
  error?: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label className="flex items-center gap-2 text-xs font-semibold text-muted-foreground">
        {label}
        {hint && <span className="text-[11px] font-normal text-muted/80">{hint}</span>}
      </label>
      {children}
      {error && <p className="text-xs text-danger">{error}</p>}
    </div>
  );
}

// ---------------------------------------------------------------------------
// JobSubmitDrawer
// ---------------------------------------------------------------------------

interface JobSubmitDrawerProps {
  open: boolean;
  onClose: () => void;
  onSuccess?: (result: SubmitJobResponse) => void;
}

export function JobSubmitDrawer({ open, onClose, onSuccess }: JobSubmitDrawerProps) {
  const submitJob = useSubmitJob();
  const topics = useTopics();
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const {
    register,
    control,
    handleSubmit,
    clearErrors,
    setError,
    setValue,
    reset,
    formState: { errors },
  } = useForm<FormValues>({
    resolver: zodResolver(submitJobSchema) as any,
    defaultValues: DEFAULT_VALUES,
  });

  const resetForm = useCallback(() => {
    reset(DEFAULT_VALUES);
    clearErrors();
    setSubmitError(null);
    setAdvancedOpen(false);
  }, [clearErrors, reset]);

  const handleClose = useCallback(() => {
    if (submitJob.isPending) return;
    resetForm();
    onClose();
  }, [onClose, resetForm, submitJob.isPending]);

  const onSubmit = handleSubmit(async (values) => {
    setSubmitError(null);
    clearErrors();

    const capabilities = dedupe(values.capabilities);
    const riskTags = dedupe(values.risk_tags);
    const contextHints = dedupe(values.context_hints);
    const workflowId = cleanOptional(values.workflow_id);
    const budget = values.budget ? Number.parseInt(values.budget, 10) : undefined;

    const labels: Record<string, string> = {};
    for (const pair of values.labels) {
      const key = pair.key.trim();
      if (!key) continue;
      labels[key] = pair.value.trim();
    }
    if (workflowId && !labels.workflow_id) {
      labels.workflow_id = workflowId;
    }

    const context: Record<string, unknown> = {};
    if (workflowId) {
      context.workflow_id = workflowId;
    }
    if (contextHints.length > 0) {
      context.context_hints = contextHints;
    }

    const payload: SubmitJobInput = {
      topic: values.topic.trim(),
      prompt: values.prompt.trim(),
      priority: values.priority,
      capability: capabilities[0],
      requires: capabilities.length > 0 ? capabilities : undefined,
      risk_tags: riskTags.length > 0 ? riskTags : undefined,
      labels: Object.keys(labels).length > 0 ? labels : undefined,
      adapter_id: cleanOptional(values.adapter_id),
      memory_id: cleanOptional(values.memory_id),
      pack_id: cleanOptional(values.pack_id),
      idempotency_key: cleanOptional(values.idempotency_key),
      max_total_tokens: budget,
      tags: contextHints.length > 0 ? contextHints : undefined,
      context: Object.keys(context).length > 0 ? context : undefined,
    };

    try {
      const result = await submitJob.mutateAsync(payload);
      resetForm();
      onSuccess?.(result);
    } catch (error) {
      handleSubmitError(error, setError, setSubmitError);
    }
  });

  return (
    <Drawer open={open} onClose={handleClose} size="lg">
      <div className="flex h-full flex-col">
        <div className="mb-5 flex items-center justify-between">
          <div>
            <h2 className="font-display text-lg font-semibold text-ink">Submit New Job</h2>
            <p className="text-xs text-muted-foreground">Create and dispatch a new agent job.</p>
          </div>
          <button
            type="button"
            onClick={handleClose}
            disabled={submitJob.isPending}
            className="rounded-full p-1.5 text-muted-foreground transition hover:bg-surface2 disabled:opacity-50"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <form onSubmit={onSubmit} className="flex flex-1 flex-col overflow-hidden">
          <div className="space-y-4 overflow-y-auto pr-1">
            {submitError && (
              <div className="rounded-xl border border-danger/30 bg-danger/5 px-3 py-2 text-xs text-danger">
                {submitError}
              </div>
            )}

            <Field label="Topic" hint="required" error={errors.topic?.message}>
              <Controller
                control={control}
                name="topic"
                render={({ field }) => (
                  <ComboboxInput
                    value={field.value}
                    onChange={field.onChange}
                    suggestions={topics}
                    placeholder="job.default"
                  />
                )}
              />
            </Field>

            <Field label="Prompt" hint="required" error={errors.prompt?.message}>
              <Textarea
                {...register("prompt")}
                rows={6}
                placeholder="Describe what the agent should do..."
              />
            </Field>

            <Field label="Priority">
              <Select {...register("priority")}>
                <option value="normal">Normal</option>
                <option value="high">High</option>
                <option value="critical">Critical</option>
                <option value="low">Low</option>
              </Select>
            </Field>

            <Field label="Capabilities">
              <Controller
                control={control}
                name="capabilities"
                render={({ field }) => (
                  <TagInput
                    value={field.value}
                    onChange={field.onChange}
                    placeholder="e.g. code_execution, web_browse"
                  />
                )}
              />
            </Field>

            <Field label="Risk Tags">
              <Controller
                control={control}
                name="risk_tags"
                render={({ field }) => (
                  <TagInput
                    value={field.value}
                    onChange={field.onChange}
                    placeholder="e.g. data_access, external_api"
                  />
                )}
              />
            </Field>

            <Field label="Metadata Labels">
              <Controller
                control={control}
                name="labels"
                render={({ field }) => (
                  <KeyValueEditor
                    value={field.value}
                    onChange={field.onChange}
                    keyPlaceholder="label key"
                    valuePlaceholder="label value"
                  />
                )}
              />
            </Field>

            <div className="border-t border-border pt-2">
              <button
                type="button"
                onClick={() => setAdvancedOpen((v) => !v)}
                className="flex items-center gap-1 text-xs font-semibold text-muted-foreground transition hover:text-ink"
              >
                {advancedOpen ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
                Advanced
              </button>
            </div>

            {advancedOpen && (
              <div className="space-y-4 rounded-xl border border-border bg-surface2/30 p-3">
                <Field label="Adapter ID" error={errors.adapter_id?.message}>
                  <Input {...register("adapter_id")} placeholder="optional adapter id" />
                </Field>

                <Field label="Memory ID" error={errors.memory_id?.message}>
                  <Input {...register("memory_id")} placeholder="optional memory id" />
                </Field>

                <Field label="Budget" hint="max total tokens" error={errors.budget?.message}>
                  <Input {...register("budget")} inputMode="numeric" placeholder="e.g. 8000" />
                </Field>

                <Field label="Context Hints">
                  <Controller
                    control={control}
                    name="context_hints"
                    render={({ field }) => (
                      <TagInput
                        value={field.value}
                        onChange={field.onChange}
                        placeholder="e.g. include_recent_history"
                      />
                    )}
                  />
                </Field>

                <Field label="Workflow ID" error={errors.workflow_id?.message}>
                  <Input {...register("workflow_id")} placeholder="optional workflow id" />
                </Field>

                <Field label="Pack ID" error={errors.pack_id?.message}>
                  <Input {...register("pack_id")} placeholder="optional pack id" />
                </Field>

                <Field label="Idempotency Key" error={errors.idempotency_key?.message}>
                  <div className="flex gap-2">
                    <Input {...register("idempotency_key")} placeholder="optional idempotency key" />
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() =>
                        setValue("idempotency_key", crypto.randomUUID(), {
                          shouldDirty: true,
                          shouldTouch: true,
                        })
                      }
                    >
                      <RefreshCw className="h-3.5 w-3.5" />
                      Generate
                    </Button>
                  </div>
                </Field>
              </div>
            )}
          </div>

          <div className="mt-5 flex items-center justify-end gap-2 border-t border-border pt-4">
            <Button type="button" variant="ghost" onClick={handleClose} disabled={submitJob.isPending}>
              Cancel
            </Button>
            <Button type="submit" disabled={submitJob.isPending}>
              {submitJob.isPending ? (
                <>
                  <Loader className="h-3.5 w-3.5 animate-spin" />
                  Submitting...
                </>
              ) : (
                "Submit Job"
              )}
            </Button>
          </div>
        </form>
      </div>
    </Drawer>
  );
}
