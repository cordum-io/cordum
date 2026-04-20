import { useState, useCallback, useEffect, useRef, useMemo, lazy, Suspense } from "react";
import { useForm, Controller, type Resolver } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import {
  Check,
  Shield,
  ShieldOff,
  Clock,
  AlertTriangle,
  ChevronDown,
  ChevronUp,
  Plus,
  X,
  Clipboard,
  CheckCircle,
} from "lucide-react";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import { Textarea } from "../ui/Textarea";
import { cn } from "../../lib/utils";
import type { PolicyRule } from "../../api/types";
import { usePolicyRules } from "../../hooks/usePolicies";
import { ConditionGroupBuilder } from "./ConditionGroupBuilder";
import { ImpactPreview } from "./ImpactPreview";
import {
  type ConditionGroup,
  fromRule,
  toMatchCriteria,
  toYaml,
  createConditionGroup,
  createCondition,
} from "./conditionTypes";

const MonacoEditor = lazy(() => import("@monaco-editor/react"));

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

const ruleSchema = z.object({
  decisionType: z.enum(["allow", "deny", "require_approval", "throttle"]),
  reason: z.string().min(1, "Reason is required"),
  maxPerMinute: z.coerce.number().min(1).optional(),
  burstLimit: z.coerce.number().min(1).optional(),
  priority: z.coerce.number().min(1).max(1000).optional(),
  ttl: z.string().optional(),
  description: z.string().optional(),
});

type RuleFormData = z.infer<typeof ruleSchema>;

// ---------------------------------------------------------------------------
// Decision options
// ---------------------------------------------------------------------------

const decisions = [
  { value: "allow" as const, label: "Allow", icon: Check, color: "border-success text-success bg-[color:rgba(31,122,87,0.08)]", active: "border-success bg-[color:rgba(31,122,87,0.18)] text-success ring-2 ring-success/30" },
  { value: "deny" as const, label: "Deny", icon: ShieldOff, color: "border-[var(--color-governance)] text-[var(--color-governance)] bg-[var(--color-governance)]/8", active: "border-[var(--color-governance)] bg-[var(--color-governance)]/18 text-[var(--color-governance)] ring-2 ring-[var(--color-governance)]/30" },
  { value: "require_approval" as const, label: "Require Approval", icon: Shield, color: "border-warning text-warning bg-[color:rgba(197,138,28,0.08)]", active: "border-warning bg-[color:rgba(197,138,28,0.18)] text-warning ring-2 ring-warning/30" },
  { value: "throttle" as const, label: "Throttle", icon: Clock, color: "border-accent text-accent bg-[color:rgba(15,127,122,0.08)]", active: "border-accent bg-[color:rgba(15,127,122,0.18)] text-accent ring-2 ring-accent/30" },
] as const;

// ---------------------------------------------------------------------------
// YAML builder
// ---------------------------------------------------------------------------

function buildYaml(
  group: ConditionGroup,
  data: Partial<RuleFormData>,
  labels: string[],
): string {
  const lines: string[] = [];
  lines.push(toYaml(group, data.decisionType ?? "allow", data.reason ?? ""));
  if (data.priority) lines.push(`priority: ${data.priority}`);
  if (data.ttl) lines.push(`ttl: ${data.ttl}`);
  if (data.description) lines.push(`description: "${data.description}"`);
  if (labels.length > 0) lines.push(`labels: [${labels.join(", ")}]`);
  if (data.decisionType === "throttle" && data.maxPerMinute) {
    lines.push(`throttle:`);
    lines.push(`  maxPerMinute: ${data.maxPerMinute}`);
    if (data.burstLimit) lines.push(`  burstLimit: ${data.burstLimit}`);
  }
  return lines.join("\n");
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export interface RuleEditorSaveData {
  matchCriteria: { capabilities: string[]; riskTags: string[]; logic: "AND" | "OR"; groups?: ConditionGroup[] };
  logic: string;
  decisionType: PolicyRule["decisionType"];
  reason: string;
  throttleConfig?: { maxPerMinute: number; burstLimit?: number };
  priority?: number;
  ttl?: string;
  description?: string;
  labels?: string[];
}

interface RuleEditorProps {
  rule?: PolicyRule;
  onSave: (data: RuleEditorSaveData) => void;
  onCancel: () => void;
}

export function RuleEditor({ rule, onSave, onCancel }: RuleEditorProps) {
  // Condition group state
  const [conditionGroup, setConditionGroup] = useState<ConditionGroup>(() =>
    rule ? fromRule(rule) : createConditionGroup("AND", [createCondition()]),
  );

  const existingThrottle = rule?.matchCriteria?.throttleConfig as
    | { maxPerMinute?: number; burstLimit?: number }
    | undefined;
  const existingLabels = (rule?.source?.labels as string[] | undefined) ?? [];

  const { register, handleSubmit, control, watch, formState: { errors } } = useForm<RuleFormData>({
    resolver: zodResolver(ruleSchema) as Resolver<RuleFormData>,
    defaultValues: {
      decisionType: (rule?.decisionType ?? "allow") as RuleFormData["decisionType"],
      reason: rule?.reason ?? "",
      maxPerMinute: existingThrottle?.maxPerMinute ?? 60,
      burstLimit: existingThrottle?.burstLimit,
      priority: rule?.priority,
      ttl: (rule?.source?.ttl as string | undefined) ?? "",
      description: (rule?.source?.description as string | undefined) ?? "",
    },
  });

  const watchAll = watch();
  const watchDecision = watchAll.decisionType;

  // Advanced section
  const [showAdvanced, setShowAdvanced] = useState(false);

  // Labels state (managed outside RHF)
  const [labels, setLabels] = useState<string[]>(existingLabels);
  const [labelDraft, setLabelDraft] = useState("");

  const addLabel = useCallback(() => {
    const trimmed = labelDraft.trim();
    if (trimmed && !labels.includes(trimmed)) {
      setLabels((prev) => [...prev, trimmed]);
    }
    setLabelDraft("");
  }, [labelDraft, labels]);

  const removeLabel = useCallback((tag: string) => {
    setLabels((prev) => prev.filter((l) => l !== tag));
  }, []);

  // YAML preview (debounced)
  const [yamlPreview, setYamlPreview] = useState("");
  const debounceRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => {
      setYamlPreview(buildYaml(conditionGroup, watchAll, labels));
    }, 300);
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
    };
  }, [conditionGroup, watchAll, labels]);

  // Copy YAML
  const [copied, setCopied] = useState(false);
  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(yamlPreview).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }, [yamlPreview]);

  // Duplicate detection
  const { data: rulesData } = usePolicyRules();
  const existingRules = rulesData?.items ?? [];
  const [duplicateWarning, setDuplicateWarning] = useState("");

  const handleGroupChange = useCallback((updated: ConditionGroup) => {
    setConditionGroup(updated);
  }, []);

  // Live criteria for impact preview
  const liveCriteria = useMemo(() => toMatchCriteria(conditionGroup), [conditionGroup]);

  const onSubmit = (data: RuleFormData) => {
    // Duplicate check
    const mc = toMatchCriteria(conditionGroup);
    const dupRule = existingRules.find((r) => {
      if (rule && r.id === rule.id) return false;
      const rCaps = (r.matchCriteria?.capabilities as string[] | undefined) ?? [];
      const rTags = (r.matchCriteria?.riskTags as string[] | undefined) ?? [];
      return (
        JSON.stringify([...mc.capabilities].sort()) === JSON.stringify([...rCaps].sort()) &&
        JSON.stringify([...mc.riskTags].sort()) === JSON.stringify([...rTags].sort()) &&
        mc.logic === (r.logic ?? "AND")
      );
    });

    if (dupRule) {
      setDuplicateWarning(`A similar rule already exists (${dupRule.id.slice(0, 10)}). Saving will create a duplicate.`);
    } else {
      setDuplicateWarning("");
    }

    const result: RuleEditorSaveData = {
      matchCriteria: mc,
      logic: mc.logic,
      decisionType: data.decisionType,
      reason: data.reason,
    };
    if (data.decisionType === "throttle" && data.maxPerMinute) {
      result.throttleConfig = {
        maxPerMinute: data.maxPerMinute,
        burstLimit: data.burstLimit || undefined,
      };
    }
    if (data.priority) result.priority = data.priority;
    if (data.ttl) result.ttl = data.ttl;
    if (data.description) result.description = data.description;
    if (labels.length > 0) result.labels = labels;
    onSave(result);
  };

  return (
    <div className="list-row animate-scale-in border-accent/30">
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {/* Left: form */}
        <form
          onSubmit={handleSubmit(onSubmit)}
          className="space-y-5"
        >
          <h4 className="text-xs font-semibold uppercase tracking-widest text-muted-foreground">
            {rule ? "Edit Rule" : "New Rule"}
          </h4>

          {/* Condition group builder */}
          <div>
            <span className="mb-2 block text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              Match Conditions
            </span>
            <ConditionGroupBuilder
              group={conditionGroup}
              onChange={handleGroupChange}
            />
          </div>

          {/* Decision selector */}
          <div>
            <span className="mb-2 block text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              Decision
            </span>
            <Controller
              control={control}
              name="decisionType"
              render={({ field }) => (
                <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                  {decisions.map((d) => {
                    const Icon = d.icon;
                    const isActive = field.value === d.value;
                    return (
                      <button
                        key={d.value}
                        type="button"
                        className={cn(
                          "flex flex-col items-center gap-1.5 rounded-2xl border px-3 py-3 text-xs font-semibold transition",
                          isActive ? d.active : d.color,
                        )}
                        onClick={() => field.onChange(d.value)}
                      >
                        <Icon className="h-5 w-5" />
                        {d.label}
                      </button>
                    );
                  })}
                </div>
              )}
            />
            {errors.decisionType && (
              <p className="mt-1 text-xs text-danger">{errors.decisionType.message}</p>
            )}
          </div>

          {/* Throttle config */}
          {watchDecision === "throttle" && (
            <div className="grid gap-4 sm:grid-cols-2">
              <div>
                <label htmlFor="re-max-per-min" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  Max per minute
                </label>
                <Input id="re-max-per-min" type="number" min={1} placeholder="60" {...register("maxPerMinute")} />
                {errors.maxPerMinute && <p className="mt-1 text-xs text-danger">{errors.maxPerMinute.message}</p>}
              </div>
              <div>
                <label htmlFor="re-burst" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  Burst limit (optional)
                </label>
                <Input id="re-burst" type="number" min={1} placeholder="10" {...register("burstLimit")} />
              </div>
            </div>
          )}

          {/* Reason */}
          <div>
            <label htmlFor="re-reason" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              Reason
            </label>
            <Textarea
              id="re-reason"
              placeholder="Why this decision? e.g. 'PII access requires human approval'"
              rows={2}
              {...register("reason")}
            />
            {errors.reason && <p className="mt-1 text-xs text-danger">{errors.reason.message}</p>}
          </div>

          {/* Advanced Options (collapsible) */}
          <div>
            <button
              type="button"
              onClick={() => setShowAdvanced((v) => !v)}
              className="flex items-center gap-1.5 text-xs font-semibold text-muted-foreground hover:text-ink transition"
            >
              {showAdvanced ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
              Advanced Options
            </button>
            {showAdvanced && (
              <div className="mt-3 space-y-4 rounded-xl border border-border bg-surface2/20 p-4">
                <div className="grid gap-4 sm:grid-cols-2">
                  <div>
                    <label htmlFor="re-priority" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                      Priority
                    </label>
                    <Input id="re-priority" type="number" min={1} max={1000} placeholder="100" {...register("priority")} />
                    <p className="mt-1 text-xs text-muted-foreground">Lower number = higher priority (1-1000)</p>
                    {errors.priority && <p className="mt-1 text-xs text-danger">{errors.priority.message}</p>}
                  </div>
                  <div>
                    <label htmlFor="re-ttl" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                      TTL
                    </label>
                    <Input id="re-ttl" placeholder="24h, 7d, 30d" {...register("ttl")} />
                    <p className="mt-1 text-xs text-muted-foreground">How long this rule stays active</p>
                  </div>
                </div>
                <div>
                  <label htmlFor="re-desc" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    Description
                  </label>
                  <Textarea id="re-desc" rows={2} placeholder="Human-readable description..." {...register("description")} />
                </div>
                <div>
                  <label className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    Labels
                  </label>
                  <div className="flex gap-2">
                    <Input
                      value={labelDraft}
                      onChange={(e) => setLabelDraft(e.target.value)}
                      onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); addLabel(); } }}
                      placeholder="Add label..."
                      className="flex-1"
                    />
                    <Button variant="outline" size="sm" type="button" onClick={addLabel}>
                      <Plus className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                  {labels.length > 0 && (
                    <div className="mt-2 flex flex-wrap gap-1.5">
                      {labels.map((l) => (
                        <button
                          key={l}
                          type="button"
                          onClick={() => removeLabel(l)}
                          className="inline-flex items-center gap-0.5 rounded-full border border-border px-2 py-0.5 text-xs font-medium text-ink transition hover:border-danger hover:text-danger"
                        >
                          {l}
                          <X className="h-2.5 w-2.5" />
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            )}
          </div>

          {/* Duplicate warning */}
          {duplicateWarning && (
            <div className="flex items-center gap-1.5 rounded-xl border border-warning/40 bg-warning/5 px-3 py-2 text-xs text-warning">
              <AlertTriangle className="h-3.5 w-3.5 flex-shrink-0" />
              {duplicateWarning}
            </div>
          )}

          {/* Validation hint */}
          {errors.root && (
            <div className="flex items-center gap-1.5 text-xs text-danger">
              <AlertTriangle className="h-3 w-3" />
              {errors.root.message}
            </div>
          )}

          {/* Actions — sticky footer so save is always visible */}
          <div className="sticky bottom-0 -mx-4 mt-2 flex items-center gap-2 border-t border-border bg-surface1/95 px-4 py-3 backdrop-blur-sm">
            <Button type="submit">
              <Check className="h-4 w-4" />
              {rule ? "Update Rule" : "Save Rule"}
            </Button>
            <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
              Cancel
            </Button>
          </div>
        </form>

        {/* Right: YAML preview */}
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              YAML Preview
            </span>
            <Button variant="ghost" size="sm" type="button" onClick={handleCopy}>
              {copied ? (
                <>
                  <CheckCircle className="h-3.5 w-3.5 text-success" />
                  <span className="text-success">Copied!</span>
                </>
              ) : (
                <>
                  <Clipboard className="h-3.5 w-3.5" />
                  Copy YAML
                </>
              )}
            </Button>
          </div>
          <div className="min-h-[300px] overflow-hidden rounded-xl border border-border">
            <Suspense
              fallback={
                <div className="flex h-[300px] items-center justify-center text-xs text-muted-foreground">
                  Loading editor...
                </div>
              }
            >
              <MonacoEditor
                height="300px"
                language="yaml"
                value={yamlPreview}
                options={{
                  readOnly: true,
                  minimap: { enabled: false },
                  lineNumbers: "off",
                  scrollBeyondLastLine: false,
                  folding: false,
                  fontSize: 12,
                  padding: { top: 12 },
                }}
                theme="vs-dark"
              />
            </Suspense>
          </div>
        </div>
      </div>

      {/* Impact Preview */}
      <ImpactPreview
        capabilities={liveCriteria.capabilities}
        riskTags={liveCriteria.riskTags}
        logic={liveCriteria.logic}
        decisionType={watchDecision}
      />
    </div>
  );
}
