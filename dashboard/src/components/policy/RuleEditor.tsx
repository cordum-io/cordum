import { useState } from "react";
import { useForm, Controller } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Check, Shield, ShieldOff, Clock, AlertTriangle } from "lucide-react";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import { Textarea } from "../ui/Textarea";
import { cn } from "../../lib/utils";
import type { PolicyRule } from "../../api/types";

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

const ruleSchema = z.object({
  capabilities: z.string(),
  riskTags: z.string(),
  logic: z.enum(["AND", "OR"]),
  decisionType: z.enum(["allow", "deny", "require_approval", "throttle"]),
  reason: z.string().min(1, "Reason is required"),
});

type RuleFormData = z.infer<typeof ruleSchema>;

// ---------------------------------------------------------------------------
// Decision options
// ---------------------------------------------------------------------------

const decisions = [
  { value: "allow" as const, label: "Allow", icon: Check, color: "border-success text-success bg-[color:rgba(31,122,87,0.08)]", active: "border-success bg-[color:rgba(31,122,87,0.18)] text-success ring-2 ring-success/30" },
  { value: "deny" as const, label: "Deny", icon: ShieldOff, color: "border-danger text-danger bg-[color:rgba(184,58,58,0.08)]", active: "border-danger bg-[color:rgba(184,58,58,0.18)] text-danger ring-2 ring-danger/30" },
  { value: "require_approval" as const, label: "Require Approval", icon: Shield, color: "border-warning text-warning bg-[color:rgba(197,138,28,0.08)]", active: "border-warning bg-[color:rgba(197,138,28,0.18)] text-warning ring-2 ring-warning/30" },
  { value: "throttle" as const, label: "Throttle", icon: Clock, color: "border-accent text-accent bg-[color:rgba(15,127,122,0.08)]", active: "border-accent bg-[color:rgba(15,127,122,0.18)] text-accent ring-2 ring-accent/30" },
] as const;

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

interface RuleEditorProps {
  rule?: PolicyRule;
  onSave: (data: { matchCriteria: { capabilities: string[]; riskTags: string[] }; logic: string; decisionType: PolicyRule["decisionType"]; reason: string }) => void;
  onCancel: () => void;
}

export function RuleEditor({ rule, onSave, onCancel }: RuleEditorProps) {
  const existingCaps = (rule?.matchCriteria?.capabilities as string[] | undefined) ?? [];
  const existingTags = (rule?.matchCriteria?.riskTags as string[] | undefined) ?? [];

  const { register, handleSubmit, control, formState: { errors } } = useForm<RuleFormData>({
    resolver: zodResolver(ruleSchema),
    defaultValues: {
      capabilities: existingCaps.join(", "),
      riskTags: existingTags.join(", "),
      logic: (rule?.logic as "AND" | "OR") || "AND",
      decisionType: rule?.decisionType ?? "allow",
      reason: rule?.reason ?? "",
    },
  });

  const [capInput, setCapInput] = useState(existingCaps.join(", "));
  const [tagInput, setTagInput] = useState(existingTags.join(", "));

  const onSubmit = (data: RuleFormData) => {
    const caps = data.capabilities
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    const tags = data.riskTags
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    onSave({
      matchCriteria: { capabilities: caps, riskTags: tags },
      logic: data.logic,
      decisionType: data.decisionType,
      reason: data.reason,
    });
  };

  return (
    <form
      onSubmit={handleSubmit(onSubmit)}
      className="list-row animate-scale-in space-y-5 border-accent/30"
    >
      <h4 className="text-xs font-semibold uppercase tracking-widest text-muted">
        {rule ? "Edit Rule" : "New Rule"}
      </h4>

      {/* Match conditions */}
      <div className="grid gap-4 sm:grid-cols-2">
        <div>
          <label htmlFor="re-caps" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted">
            Capabilities
          </label>
          <Input
            id="re-caps"
            placeholder="e.g. file_write, shell_exec"
            value={capInput}
            {...register("capabilities")}
            onChange={(e) => {
              setCapInput(e.target.value);
              register("capabilities").onChange(e);
            }}
          />
          <p className="mt-1 text-[10px] text-muted">Comma-separated</p>
        </div>
        <div>
          <label htmlFor="re-tags" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted">
            Risk Tags
          </label>
          <Input
            id="re-tags"
            placeholder="e.g. pii, financial, external"
            value={tagInput}
            {...register("riskTags")}
            onChange={(e) => {
              setTagInput(e.target.value);
              register("riskTags").onChange(e);
            }}
          />
          <p className="mt-1 text-[10px] text-muted">Comma-separated</p>
        </div>
      </div>

      {/* Logic toggle */}
      <Controller
        control={control}
        name="logic"
        render={({ field }) => (
          <div className="flex items-center gap-3">
            <span className="text-xs font-semibold uppercase tracking-wide text-muted">Match</span>
            <div className="flex rounded-full border border-border">
              <button
                type="button"
                className={cn(
                  "rounded-l-full px-4 py-1.5 text-xs font-semibold transition",
                  field.value === "AND"
                    ? "bg-accent/15 text-accent"
                    : "text-muted hover:text-ink",
                )}
                onClick={() => field.onChange("AND")}
              >
                All of these
              </button>
              <button
                type="button"
                className={cn(
                  "rounded-r-full px-4 py-1.5 text-xs font-semibold transition",
                  field.value === "OR"
                    ? "bg-accent/15 text-accent"
                    : "text-muted hover:text-ink",
                )}
                onClick={() => field.onChange("OR")}
              >
                Any of these
              </button>
            </div>
          </div>
        )}
      />

      {/* Decision selector */}
      <div>
        <span className="mb-2 block text-xs font-semibold uppercase tracking-wide text-muted">
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

      {/* Reason */}
      <div>
        <label htmlFor="re-reason" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted">
          Reason
        </label>
        <Textarea
          id="re-reason"
          placeholder="Why this decision? e.g. 'PII access requires human approval'"
          rows={2}
          {...register("reason")}
        />
        {errors.reason && (
          <p className="mt-1 text-xs text-danger">{errors.reason.message}</p>
        )}
      </div>

      {/* Validation hint */}
      {errors.root && (
        <div className="flex items-center gap-1.5 text-xs text-danger">
          <AlertTriangle className="h-3 w-3" />
          {errors.root.message}
        </div>
      )}

      {/* Actions */}
      <div className="flex items-center gap-2 pt-1">
        <Button type="submit" size="sm">
          {rule ? "Update Rule" : "Add Rule"}
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
