import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { Loader2, RotateCw, X } from "lucide-react";
import type { Job, JobPriority, RemediateJobInput } from "../../api/types";
import { useRemediateJob } from "../../hooks/useJobs";
import { useMemory } from "../../hooks/useMemory";
import { useToastStore } from "../../state/toast";
import { cn } from "../../lib/utils";
import { Drawer } from "../ui/Drawer";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import { Select } from "../ui/Select";
import { Textarea } from "../ui/Textarea";
import { TagInput } from "../ui/TagInput";
import { KeyValueEditor } from "../ui/KeyValueEditor";

interface RemediateDrawerProps {
  jobId: string;
  originalJob: Job;
  open: boolean;
  onClose: () => void;
}

interface LabelPair {
  key: string;
  value: string;
}

interface RemediateFormState {
  topic: string;
  prompt: string;
  priority: JobPriority;
  capabilities: string[];
  riskTags: string[];
  labels: LabelPair[];
  reason: string;
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return undefined;
  }
  return value as Record<string, unknown>;
}

function pickPromptFromMemoryJson(raw: unknown): string {
  const record = asRecord(raw);
  if (!record) return "";
  if (typeof record.prompt === "string" && record.prompt.trim()) {
    return record.prompt.trim();
  }
  const context = asRecord(record.context);
  if (context && typeof context.prompt === "string" && context.prompt.trim()) {
    return context.prompt.trim();
  }
  return "";
}

function stringifyLabelValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (value === undefined || value === null) return "";
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return "";
  }
}

function labelsToPairs(labels: Record<string, unknown>): LabelPair[] {
  return Object.entries(labels)
    .map(([key, value]) => ({
      key,
      value: stringifyLabelValue(value),
    }))
    .filter((pair) => pair.key.trim() !== "");
}

function normalizeTags(values: string[]): string[] {
  return Array.from(
    new Set(values.map((value) => value.trim()).filter(Boolean)),
  );
}

function labelPairsToRecord(pairs: LabelPair[]): Record<string, string> {
  const out: Record<string, string> = {};
  pairs.forEach((pair) => {
    const key = pair.key.trim();
    if (!key) return;
    out[key] = pair.value.trim();
  });
  return out;
}

function sorted(values: string[]): string[] {
  return [...values].sort((a, b) => a.localeCompare(b));
}

function arraysEqual(a: string[], b: string[]): boolean {
  const left = sorted(normalizeTags(a));
  const right = sorted(normalizeTags(b));
  if (left.length !== right.length) return false;
  return left.every((value, index) => value === right[index]);
}

function recordsEqual(a: Record<string, string>, b: Record<string, string>): boolean {
  const aKeys = Object.keys(a).sort();
  const bKeys = Object.keys(b).sort();
  if (aKeys.length !== bKeys.length) return false;
  return aKeys.every((key, index) => key === bKeys[index] && a[key] === b[key]);
}

function fieldChangedStyle(changed: boolean): string {
  return changed
    ? "border-accent/50 bg-accent/5"
    : "border-border/60 bg-card/30";
}

function buildInitialState(job: Job, prompt: string): RemediateFormState {
  const metadata = asRecord(job.metadata) ?? {};
  const labelsFromMetadata = asRecord(metadata.labels) ?? {};
  const priorityRaw = String(metadata.priority ?? "normal").toLowerCase();
  const priority: JobPriority =
    priorityRaw === "low" || priorityRaw === "high" || priorityRaw === "critical"
      ? priorityRaw
      : "normal";

  return {
    topic: job.topic || "",
    prompt,
    priority,
    capabilities: normalizeTags(job.capabilities ?? []),
    riskTags: normalizeTags(job.riskTags ?? []),
    labels: labelsToPairs(labelsFromMetadata),
    reason: "",
  };
}

function DiffField({
  title,
  changed,
  children,
}: {
  title: string;
  changed: boolean;
  children: ReactNode;
}) {
  return (
    <div className={cn("rounded-2xl border p-3", fieldChangedStyle(changed))}>
      <div className="mb-2 flex items-center justify-between">
        <p className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{title}</p>
        {changed && <span className="h-2.5 w-2.5 rounded-full bg-accent" />}
      </div>
      {children}
    </div>
  );
}

export function RemediateDrawer({ jobId, originalJob, open, onClose }: RemediateDrawerProps) {
  const navigate = useNavigate();
  const remediate = useRemediateJob();
  const addToast = useToastStore((state) => state.addToast);
  const { data: memoryData } = useMemory(open ? originalJob.contextPtr : undefined);
  const memoryPrompt = pickPromptFromMemoryJson(memoryData?.json);

  const baseline = useMemo(() => {
    const prompt =
      memoryPrompt ||
      (typeof originalJob.metadata?.prompt === "string" ? originalJob.metadata.prompt : "");
    return buildInitialState(originalJob, prompt);
  }, [memoryPrompt, originalJob]);

  const [form, setForm] = useState<RemediateFormState>(baseline);
  const [submitError, setSubmitError] = useState<string>("");

  useEffect(() => {
    if (!open) return;
    setForm(baseline);
    setSubmitError("");
  }, [open, baseline, jobId]);

  const changed = useMemo(() => {
    const baselineLabels = labelPairsToRecord(baseline.labels);
    const currentLabels = labelPairsToRecord(form.labels);
    return {
      topic: form.topic.trim() !== baseline.topic.trim(),
      prompt: form.prompt.trim() !== baseline.prompt.trim(),
      priority: form.priority !== baseline.priority,
      capabilities: !arraysEqual(form.capabilities, baseline.capabilities),
      riskTags: !arraysEqual(form.riskTags, baseline.riskTags),
      labels: !recordsEqual(currentLabels, baselineLabels),
    };
  }, [baseline, form]);

  const modifiedCount = useMemo(
    () => Object.values(changed).filter(Boolean).length,
    [changed],
  );

  const handleClose = () => {
    if (remediate.isPending) return;
    onClose();
  };

  const handleSubmit = async () => {
    setSubmitError("");
    const reason = form.reason.trim();
    if (!reason) {
      setSubmitError("Reason for remediation is required.");
      return;
    }

    const capabilities = normalizeTags(form.capabilities);
    const riskTags = normalizeTags(form.riskTags);
    const labels = labelPairsToRecord(form.labels);

    const payload: RemediateJobInput = {
      topic: form.topic.trim() || undefined,
      prompt: form.prompt.trim() || undefined,
      priority: form.priority,
      capability: capabilities[0],
      requires: capabilities.length > 0 ? capabilities : undefined,
      risk_tags: riskTags.length > 0 ? riskTags : undefined,
      labels: Object.keys(labels).length > 0 ? labels : undefined,
      reason,
    };

    try {
      const result = await remediate.mutateAsync({
        jobId,
        input: payload,
      });
      addToast({
        type: "success",
        title: "Job remediated",
        description: `New job ${result.job_id.slice(0, 8)} created`,
      });
      onClose();
      navigate(`/jobs/${result.job_id}`);
    } catch (error) {
      setSubmitError(error instanceof Error ? error.message : "Failed to remediate job");
    }
  };

  return (
    <Drawer open={open} onClose={handleClose} size="lg">
      <div className="flex h-full flex-col">
        <div className="mb-4 flex items-start justify-between gap-3">
          <div>
            <h2 className="font-display text-lg font-semibold text-ink">Remediate Job</h2>
            <p className="mt-1 text-xs text-muted-foreground">
              Remediation creates a new job linked to the original, with your modifications applied.
            </p>
          </div>
          <button
            type="button"
            onClick={handleClose}
            disabled={remediate.isPending}
            className="rounded-full p-1.5 text-muted-foreground transition hover:bg-surface2 disabled:opacity-50"
            aria-label="Close remediation drawer"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="mb-3 flex items-center justify-between rounded-xl border border-border/60 bg-surface2/25 px-3 py-2">
          <span className="text-xs text-muted-foreground">Modified fields</span>
          <span className="text-sm font-semibold text-ink">{modifiedCount}</span>
        </div>

        {submitError && (
          <div className="mb-3 rounded-xl border border-danger/30 bg-danger/5 px-3 py-2 text-xs text-danger">
            {submitError}
          </div>
        )}

        <div className="flex-1 space-y-3 overflow-y-auto pr-1">
          <DiffField title="Topic" changed={changed.topic}>
            <Input
              value={form.topic}
              onChange={(event) =>
                setForm((current) => ({ ...current, topic: event.target.value }))
              }
              placeholder="job.topic"
            />
          </DiffField>

          <DiffField title="Prompt" changed={changed.prompt}>
            <Textarea
              value={form.prompt}
              onChange={(event) =>
                setForm((current) => ({ ...current, prompt: event.target.value }))
              }
              rows={6}
              placeholder="Updated prompt"
            />
          </DiffField>

          <DiffField title="Priority" changed={changed.priority}>
            <Select
              value={form.priority}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  priority: event.target.value as JobPriority,
                }))
              }
            >
              <option value="low">Low</option>
              <option value="normal">Normal</option>
              <option value="high">High</option>
              <option value="critical">Critical</option>
            </Select>
          </DiffField>

          <DiffField title="Capabilities" changed={changed.capabilities}>
            <TagInput
              value={form.capabilities}
              onChange={(next) =>
                setForm((current) => ({ ...current, capabilities: next }))
              }
              placeholder="Add capabilities"
            />
          </DiffField>

          <DiffField title="Risk Tags" changed={changed.riskTags}>
            <TagInput
              value={form.riskTags}
              onChange={(next) =>
                setForm((current) => ({ ...current, riskTags: next }))
              }
              placeholder="Add risk tags"
            />
          </DiffField>

          <DiffField title="Labels" changed={changed.labels}>
            <KeyValueEditor
              value={form.labels}
              onChange={(next) =>
                setForm((current) => ({ ...current, labels: next }))
              }
              keyPlaceholder="label key"
              valuePlaceholder="label value"
            />
          </DiffField>

          <div className="rounded-2xl border border-warning/40 bg-warning/10 p-3">
            <label className="mb-2 block text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Reason For Remediation
            </label>
            <Textarea
              value={form.reason}
              onChange={(event) =>
                setForm((current) => ({ ...current, reason: event.target.value }))
              }
              rows={3}
              placeholder="Why are you remediating this job?"
            />
          </div>
        </div>

        <div className="mt-4 flex items-center justify-end gap-2 border-t border-border pt-4">
          <Button
            type="button"
            variant="ghost"
            onClick={handleClose}
            disabled={remediate.isPending}
          >
            Cancel
          </Button>
          <Button
            type="button"
            onClick={() => {
              void handleSubmit();
            }}
            disabled={remediate.isPending}
          >
            {remediate.isPending ? (
              <>
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                Resubmitting...
              </>
            ) : (
              <>
                <RotateCw className="h-3.5 w-3.5" />
                Remediate &amp; Resubmit
              </>
            )}
          </Button>
        </div>
      </div>
    </Drawer>
  );
}
