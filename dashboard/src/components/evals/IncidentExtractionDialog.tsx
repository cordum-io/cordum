import { useState } from "react";
import { useForm, Controller, type Resolver } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { toast } from "sonner";
import { X } from "lucide-react";
import { DialogOverlay } from "@/components/ui/DialogOverlay";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { Textarea } from "@/components/ui/Textarea";
import { Checkbox } from "@/components/ui/Checkbox";
import { LabeledField } from "@/components/ui/LabeledField";
import { CollapsibleSection } from "@/components/ui/CollapsibleSection";
import { ApiError } from "@/api/client";
import { useCreateDatasetFromIncidents } from "@/hooks/useEvals";
import {
  DEFAULT_EVAL_EXTRACT_ENTRIES,
  DEFAULT_EVAL_EXTRACT_VERDICTS,
  EVAL_DATASET_NAME_HINT,
  EVAL_DATASET_NAME_REGEX,
  MAX_EVAL_EXTRACT_ENTRIES,
} from "@/lib/evalsValidation";
import type { ExtractIncidentsPreview, SafetyDecisionType } from "@/api/types";

const ALL_VERDICTS: SafetyDecisionType[] = [
  "deny",
  "require_approval",
  "allow_with_constraints",
  "throttle",
  "allow",
];

const formSchema = z.object({
  datasetName: z
    .string()
    .trim()
    .min(3, "At least 3 characters")
    .max(64, "At most 64 characters")
    .regex(EVAL_DATASET_NAME_REGEX, EVAL_DATASET_NAME_HINT),
  description: z.string().trim().max(500).optional(),
  since: z.string().optional(),
  until: z.string().optional(),
  topicPattern: z.string().trim().optional(),
  ruleId: z.string().trim().optional(),
  agentId: z.string().trim().optional(),
  verdicts: z.array(z.string()).min(1, "Select at least one verdict"),
  maxEntries: z.coerce
    .number()
    .int("Must be an integer")
    .min(1, "At least 1")
    .max(MAX_EVAL_EXTRACT_ENTRIES, `Max ${MAX_EVAL_EXTRACT_ENTRIES}`),
  dryRun: z.boolean(),
});

type FormValues = z.infer<typeof formSchema>;

function sevenDaysAgo(): string {
  const now = new Date();
  now.setDate(now.getDate() - 7);
  return now.toISOString().slice(0, 10);
}

function today(): string {
  return new Date().toISOString().slice(0, 10);
}

function defaultValues(): FormValues {
  return {
    datasetName: "",
    description: "",
    since: sevenDaysAgo(),
    until: today(),
    topicPattern: "",
    ruleId: "",
    agentId: "",
    verdicts: [...DEFAULT_EVAL_EXTRACT_VERDICTS],
    maxEntries: DEFAULT_EVAL_EXTRACT_ENTRIES,
    dryRun: true,
  };
}

export interface IncidentExtractionDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function IncidentExtractionDialog({ open, onOpenChange }: IncidentExtractionDialogProps) {
  const [preview, setPreview] = useState<ExtractIncidentsPreview | null>(null);
  const mutation = useCreateDatasetFromIncidents();

  const {
    control,
    register,
    handleSubmit,
    reset,
    watch,
    setValue,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema) as Resolver<FormValues>,
    defaultValues: defaultValues(),
  });

  const dryRun = watch("dryRun");
  const verdicts = watch("verdicts");

  function close() {
    reset(defaultValues());
    setPreview(null);
    onOpenChange(false);
  }

  async function onSubmit(values: FormValues) {
    try {
      const result = await mutation.mutateAsync({
        datasetName: values.datasetName,
        datasetDescription: values.description || undefined,
        since: values.since ? `${values.since}T00:00:00Z` : undefined,
        until: values.until ? `${values.until}T23:59:59Z` : undefined,
        topicPattern: values.topicPattern || undefined,
        ruleId: values.ruleId || undefined,
        agentId: values.agentId || undefined,
        verdicts: values.verdicts as SafetyDecisionType[],
        maxEntries: values.maxEntries,
        dryRun: values.dryRun,
      });
      if (values.dryRun) {
        setPreview(result.preview);
        return;
      }
      toast.success(
        `Created dataset "${result.dataset?.name ?? values.datasetName}" with ${result.preview.entryCount} entries`,
      );
      close();
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        toast.error(
          "A dataset with that name already exists. Pick a new name or create a new version.",
        );
        return;
      }
      toast.error(err instanceof Error ? err.message : "Failed to create dataset");
    }
  }

  function toggleVerdict(verdict: SafetyDecisionType) {
    const next = verdicts.includes(verdict)
      ? verdicts.filter((v) => v !== verdict)
      : [...verdicts, verdict];
    setValue("verdicts", next, { shouldDirty: true, shouldValidate: true });
  }

  return (
    <DialogOverlay
      open={open}
      onClose={close}
      label="Create eval dataset from incidents"
      className="w-full max-w-lg rounded-2xl border border-border bg-surface-1 shadow-xl"
      initialFocusSelector='input[name="datasetName"]'
    >
      <div className="flex items-center justify-between border-b border-border px-6 py-4">
        <div>
          <h2 className="font-display text-lg font-semibold text-foreground">
            Create dataset from incidents
          </h2>
          <p className="mt-0.5 text-xs text-muted-foreground">
            Denied + approval-required actions become test cases.
          </p>
        </div>
        <button
          type="button"
          aria-label="Close dialog"
          onClick={close}
          className="text-muted-foreground hover:text-foreground"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      <form
        onSubmit={handleSubmit(onSubmit)}
        className="max-h-[70vh] space-y-4 overflow-y-auto px-6 py-4"
      >
        <LabeledField label="Dataset name" description={EVAL_DATASET_NAME_HINT}>
          <Input
            {...register("datasetName")}
            placeholder="denies-2026-04"
            aria-invalid={!!errors.datasetName}
          />
          {errors.datasetName && (
            <p className="mt-1 text-xs text-danger">{errors.datasetName.message}</p>
          )}
        </LabeledField>

        <LabeledField label="Description (optional)">
          <Textarea
            {...register("description")}
            rows={2}
            placeholder="Regression suite covering April denies"
          />
        </LabeledField>

        <div className="grid grid-cols-2 gap-3">
          <LabeledField label="Since">
            <Input type="date" {...register("since")} />
          </LabeledField>
          <LabeledField label="Until">
            <Input type="date" {...register("until")} />
          </LabeledField>
        </div>

        <LabeledField label="Topic pattern (glob, optional)">
          <Input {...register("topicPattern")} placeholder="fs.*" />
        </LabeledField>

        <div className="grid grid-cols-2 gap-3">
          <LabeledField label="Rule ID (optional)">
            <Input {...register("ruleId")} />
          </LabeledField>
          <LabeledField label="Agent ID (optional)">
            <Input {...register("agentId")} />
          </LabeledField>
        </div>

        <LabeledField label="Verdicts">
          <div className="flex flex-wrap gap-2">
            {ALL_VERDICTS.map((v) => (
              <Checkbox
                key={v}
                name="verdicts"
                value={v}
                checked={verdicts.includes(v)}
                onChange={() => toggleVerdict(v)}
                label={v.replace(/_/g, " ")}
              />
            ))}
          </div>
          {errors.verdicts && (
            <p className="mt-1 text-xs text-danger">{errors.verdicts.message as string}</p>
          )}
        </LabeledField>

        <LabeledField label="Max entries" description={`1 – ${MAX_EVAL_EXTRACT_ENTRIES}`}>
          <Input type="number" min={1} max={MAX_EVAL_EXTRACT_ENTRIES} {...register("maxEntries")} />
          {errors.maxEntries && (
            <p className="mt-1 text-xs text-danger">{errors.maxEntries.message}</p>
          )}
        </LabeledField>

        <Controller
          control={control}
          name="dryRun"
          render={({ field }) => (
            <Checkbox
              checked={field.value}
              onChange={(e) => field.onChange((e.target as HTMLInputElement).checked)}
              label="Dry-run preview"
              description="Count matching decisions without persisting the dataset."
            />
          )}
        />

        {preview && (
          <CollapsibleSection title="Dry-run preview" defaultOpen>
            <dl className="grid grid-cols-2 gap-3 text-xs">
              <div>
                <dt className="text-muted-foreground">Scanned decisions</dt>
                <dd className="font-mono text-foreground">{preview.scannedDecisions}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Entries</dt>
                <dd className="font-mono text-foreground">{preview.entryCount}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Deduped</dt>
                <dd className="font-mono text-foreground">{preview.dedupedCount}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Warnings</dt>
                <dd className="font-mono text-foreground">{preview.warnings.length}</dd>
              </div>
            </dl>
            {preview.warnings.length > 0 && (
              <ul className="mt-2 space-y-0.5 text-xs text-warning">
                {preview.warnings.map((w, i) => (
                  <li key={i}>• {w}</li>
                ))}
              </ul>
            )}
          </CollapsibleSection>
        )}
      </form>

      <div className="flex items-center justify-end gap-2 border-t border-border px-6 py-3">
        <Button variant="ghost" onClick={close} type="button">
          Cancel
        </Button>
        <Button
          variant="default"
          type="button"
          loading={isSubmitting || mutation.isPending}
          onClick={handleSubmit(onSubmit)}
        >
          {dryRun ? (preview ? "Preview again" : "Preview") : "Create dataset"}
        </Button>
      </div>
    </DialogOverlay>
  );
}
