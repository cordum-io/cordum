import { useEffect, useMemo, useRef, type ReactNode } from "react";
import {
  Controller,
  useForm,
  type Control,
  type FieldErrors,
  type Path,
  type UseFormRegister,
  type UseFormReturn,
} from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { Filter, GaugeCircle, Hash, ScanText, ShieldCheck, Slash, Tag } from "lucide-react";
import type { z } from "zod";
import { Input } from "@/components/ui/Input";
import { Select } from "@/components/ui/Select";
import { LabeledField } from "@/components/ui/LabeledField";
import { Checkbox } from "@/components/ui/Checkbox";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import type { NormalizedRule } from "@/hooks/useRulesList";
import {
  formValuesToRule,
  inputRuleFormSchema,
  outputRuleFormSchema,
  velocityRuleFormSchema,
  edgeRuleFormSchema,
  ruleToFormValues,
  type EdgeRuleFormValues,
  type InputRuleFormValues,
  type OutputRuleFormValues,
  type RuleFormValues,
  type VelocityRuleFormValues,
} from "@/lib/policy-studio/zod";
import { TokenInput } from "./TokenInput";

// Debounce window for Form -> canonical rule emit. Matches RuleMonacoEditor's
// 300ms so a user toggling between views sees consistent latency.
const ONCHANGE_DEBOUNCE_MS = 300;

interface RuleFormViewProps {
  rule: NormalizedRule & { type: RuleType };
  onChange: (rule: NormalizedRule) => void;
}

export function RuleFormView({ rule, onChange }: RuleFormViewProps) {
  // RuleType is a string-literal union, but `rule` is typed as the wider
  // `NormalizedRule & { type: RuleType }`; the discriminator narrows the
  // generic per-type alias only after we cast on the way into each
  // sub-form. The runtime check is the only thing that matters; the
  // casts are zero-runtime-cost.
  switch (rule.type) {
    case RuleType.input:
      return <InputRuleForm rule={rule as NormalizedRule & { type: typeof RuleType.input }} onChange={onChange} />;
    case RuleType.output:
      return <OutputRuleForm rule={rule as NormalizedRule & { type: typeof RuleType.output }} onChange={onChange} />;
    case RuleType.velocity:
      return <VelocityRuleForm rule={rule as NormalizedRule & { type: typeof RuleType.velocity }} onChange={onChange} />;
    case RuleType.edge:
      return <EdgeRuleForm rule={rule as NormalizedRule & { type: typeof RuleType.edge }} onChange={onChange} />;
    default:
      // Reachable only if a future RuleType is added without updating
      // this switch — surface a visible notice rather than render
      // nothing.
      return <UnsupportedTypeNotice />;
  }
}

export default RuleFormView;

function UnsupportedTypeNotice() {
  return (
    <div className="flex h-full items-center justify-center px-6 py-8 text-sm text-muted-foreground">
      This rule type doesn't have a structured form yet. Switch to YAML to author it directly.
    </div>
  );
}

// ---------------------------------------------------------------------------
// Shared structural-equality helper. Used to suppress no-op echoes from the
// debounced subscription so a Form -> Rule -> Form round-trip doesn't
// re-fire onChange. Object-identity guarding via lastEmittedRef is also
// kept so the parent update path is also tight.
// ---------------------------------------------------------------------------

function isStructurallyEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (a === null || b === null) return false;
  if (typeof a !== typeof b) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b)) return false;
    if (a.length !== b.length) return false;
    return a.every((value, index) => isStructurallyEqual(value, b[index]));
  }
  if (typeof a === "object") {
    const aRecord = a as Record<string, unknown>;
    const bRecord = b as Record<string, unknown>;
    const aKeys = Object.keys(aRecord);
    const bKeys = Object.keys(bRecord);
    if (aKeys.length !== bKeys.length) return false;
    return aKeys.every((key) => isStructurallyEqual(aRecord[key], bRecord[key]));
  }
  return false;
}

// ---------------------------------------------------------------------------
// Generic sync hook: encapsulates the bidirectional sync invariants every
// per-type sub-form needs.
//   - parent rule changes -> reset form (only when not echo of our own emit)
//   - form changes -> debounced safeParse -> structural equality check ->
//     onChange(canonical rule)
// ---------------------------------------------------------------------------

function useRuleFormSync<TValues extends RuleFormValues, TSchema extends z.ZodTypeAny>({
  form,
  schema,
  rule,
  onChange,
}: {
  form: UseFormReturn<TValues>;
  schema: TSchema;
  rule: NormalizedRule & { type: RuleType };
  onChange: (rule: NormalizedRule) => void;
}) {
  const lastEmittedRef = useRef<NormalizedRule>(rule);
  const ruleRef = useRef<NormalizedRule & { type: RuleType }>(rule);
  const onChangeRef = useRef(onChange);

  useEffect(() => {
    ruleRef.current = rule;
    onChangeRef.current = onChange;
  });

  // External rule update (e.g. Monaco edit, programmatic reset). Skip when
  // the incoming rule is the same object reference we just emitted; that's
  // the parent's pass-through of our own change and resetting on it would
  // re-fire the debounced subscription.
  useEffect(() => {
    if (lastEmittedRef.current === rule) return;
    const next = ruleToFormValues(rule) as unknown as TValues;
    form.reset(next, { keepDirty: false, keepErrors: false });
    lastEmittedRef.current = rule;
    // form.reset is stable across renders.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rule]);

  useEffect(() => {
    let timer: number | undefined;
    const subscription = form.watch((values) => {
      if (timer !== undefined) {
        window.clearTimeout(timer);
      }
      timer = window.setTimeout(() => {
        const result = schema.safeParse(values);
        if (!result.success) return;
        const baseRule = ruleRef.current;
        const next = formValuesToRule(result.data as RuleFormValues, baseRule);
        if (isStructurallyEqual(next, baseRule)) return;
        lastEmittedRef.current = next;
        onChangeRef.current(next);
      }, ONCHANGE_DEBOUNCE_MS);
    });
    return () => {
      if (timer !== undefined) window.clearTimeout(timer);
      subscription.unsubscribe();
    };
    // form.watch is stable; subscribing once is intentional.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [form, schema]);
}

// ---------------------------------------------------------------------------
// Shared envelope card: name, description, scope, status. All four
// per-type forms render this above their type-specific match/decide.
// ---------------------------------------------------------------------------

interface EnvelopeFieldsProps<TValues extends RuleFormValues> {
  register: UseFormRegister<TValues>;
  control: Control<TValues>;
  errors: FieldErrors<TValues>;
}

function EnvelopeFields<TValues extends RuleFormValues>({
  register,
  control: _control,
  errors,
}: EnvelopeFieldsProps<TValues>) {
  return (
    <Section title="Envelope" icon={<Tag className="h-3.5 w-3.5" />}>
      <FieldRow>
        <LabeledField
          label="Name"
          description="Shown in the rules list and audit trail."
          className="flex-1"
        >
          <Input
            type="text"
            placeholder="e.g. Block secrets in chat"
            aria-label="Rule name"
            aria-invalid={Boolean(errors.name) || undefined}
            {...register("name" as Path<TValues>)}
          />
          <FieldError message={(errors.name as { message?: string } | undefined)?.message} />
        </LabeledField>
      </FieldRow>
      <FieldRow>
        <LabeledField label="Description" description="Optional. Up to 1000 characters.">
          <Input
            type="text"
            placeholder="Why does this rule exist?"
            aria-label="Rule description"
            {...register("description" as Path<TValues>)}
          />
          <FieldError
            message={(errors.description as { message?: string } | undefined)?.message}
          />
        </LabeledField>
      </FieldRow>
      <FieldRow>
        <LabeledField label="Scope kind" className="w-44">
          <Select aria-label="Scope kind" {...register("scope.kind" as Path<TValues>)}>
            <option value={RuleScopeKind.global}>global</option>
            <option value={RuleScopeKind.tenant}>tenant</option>
            <option value={RuleScopeKind.workflow}>workflow</option>
            <option value={RuleScopeKind.edge_fleet}>edge_fleet</option>
            <option value={RuleScopeKind.edge_user}>edge_user</option>
          </Select>
          <FieldError
            message={
              ((errors.scope as { kind?: { message?: string } } | undefined)?.kind?.message) ?? undefined
            }
          />
        </LabeledField>
        <LabeledField label="Scope value" description="Required for non-global scopes." className="flex-1">
          <Input
            type="text"
            placeholder="e.g. tenant-acme"
            aria-label="Scope value"
            {...register("scope.value" as Path<TValues>)}
          />
          <FieldError
            message={
              ((errors.scope as { value?: { message?: string } } | undefined)?.value?.message) ?? undefined
            }
          />
        </LabeledField>
        <LabeledField label="Status" className="w-36">
          <Select aria-label="Rule status" {...register("status" as Path<TValues>)}>
            <option value={RuleStatus.draft}>draft</option>
            <option value={RuleStatus.published}>published</option>
            <option value={RuleStatus.deprecated}>deprecated</option>
          </Select>
          <FieldError message={(errors.status as { message?: string } | undefined)?.message} />
        </LabeledField>
      </FieldRow>
    </Section>
  );
}

// ---------------------------------------------------------------------------
// Per-type forms
// ---------------------------------------------------------------------------

interface PerTypeFormProps<T extends RuleType> {
  rule: NormalizedRule & { type: T };
  onChange: (rule: NormalizedRule) => void;
}

function InputRuleForm({ rule, onChange }: PerTypeFormProps<typeof RuleType.input>) {
  const defaultValues = useMemo(
    () => ruleToFormValues(rule) as InputRuleFormValues,
    // Initial defaultValues only — subsequent rule updates flow through reset.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );
  const form = useForm<InputRuleFormValues>({
    resolver: zodResolver(inputRuleFormSchema),
    defaultValues,
    mode: "onChange",
  });
  useRuleFormSync({ form, schema: inputRuleFormSchema, rule, onChange });

  const {
    register,
    control,
    formState: { errors },
  } = form;

  return (
    <FormShell label="Input rule editor">
      <EnvelopeFields register={register} control={control} errors={errors} />
      <Section title="Match selectors" icon={<Filter className="h-3.5 w-3.5" />}>
        <ArrayField
          name="match.topics"
          label="Topics"
          description="Topics this rule applies to (e.g. email.draft). Press Enter or comma to add."
          control={form.control as unknown as Control<RuleFormValues>}
          error={errors.match?.topics?.message}
        />
        <ArrayField
          name="match.tools"
          label="Tools"
          description="Tool names triggering this rule (e.g. Bash, Edit)."
          control={form.control as unknown as Control<RuleFormValues>}
          error={errors.match?.tools?.message}
        />
        <ArrayField
          name="match.risk_tags"
          label="Risk tags"
          description="Required risk tags on the job context for the rule to fire."
          control={form.control as unknown as Control<RuleFormValues>}
          error={errors.match?.risk_tags?.message}
        />
        <FieldRow>
          <LabeledField
            label="Content pattern"
            description="Optional regex applied to input content. Rule fires only on match."
            className="flex-1"
          >
            <Input
              type="text"
              placeholder="e.g. (?i)password\\s*="
              aria-label="Content pattern"
              {...register("match.content_pattern")}
            />
            <FieldError message={errors.match?.content_pattern?.message} />
          </LabeledField>
        </FieldRow>
      </Section>
      <Section title="Decision" icon={<ShieldCheck className="h-3.5 w-3.5" />}>
        <FieldRow>
          <LabeledField label="Decision type" className="w-56">
            <Select aria-label="Decision type" {...register("decide.type")}>
              <option value="allow">allow</option>
              <option value="deny">deny</option>
              <option value="require_human">require_human</option>
              <option value="throttle">throttle</option>
              <option value="allow_with_constraints">allow_with_constraints</option>
              <option value="quarantine">quarantine</option>
              <option value="redact">redact</option>
            </Select>
            <FieldError message={errors.decide?.type?.message} />
          </LabeledField>
          <LabeledField label="Reason" description="Logged with the decision." className="flex-1">
            <Input
              type="text"
              placeholder="Why this decision applies"
              aria-label="Decision reason"
              {...register("decide.reason")}
            />
            <FieldError message={errors.decide?.reason?.message} />
          </LabeledField>
        </FieldRow>
      </Section>
    </FormShell>
  );
}

function OutputRuleForm({ rule, onChange }: PerTypeFormProps<typeof RuleType.output>) {
  const defaultValues = useMemo(
    () => ruleToFormValues(rule) as OutputRuleFormValues,
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );
  const form = useForm<OutputRuleFormValues>({
    resolver: zodResolver(outputRuleFormSchema),
    defaultValues,
    mode: "onChange",
  });
  useRuleFormSync({ form, schema: outputRuleFormSchema, rule, onChange });

  const {
    register,
    control,
    formState: { errors },
  } = form;

  return (
    <FormShell label="Output rule editor">
      <EnvelopeFields register={register} control={control} errors={errors} />
      <Section title="Match selectors" icon={<Filter className="h-3.5 w-3.5" />}>
        <ArrayField
          name="match.topics"
          label="Topics"
          description="Topics this rule applies to."
          control={form.control as unknown as Control<RuleFormValues>}
          error={errors.match?.topics?.message}
        />
        <ArrayField
          name="match.tools"
          label="Tools"
          description="Tool names whose output is scanned by this rule."
          control={form.control as unknown as Control<RuleFormValues>}
          error={errors.match?.tools?.message}
        />
        <ArrayField
          name="match.risk_tags"
          label="Risk tags"
          description="Required risk tags on the job context."
          control={form.control as unknown as Control<RuleFormValues>}
          error={errors.match?.risk_tags?.message}
        />
        <Controller
          control={form.control}
          name="match.finding_types"
          render={({ field }) => (
            <LabeledField label="Finding types" description="Output scanner findings the rule reacts to.">
              <FindingTypeChecklist value={field.value ?? []} onChange={field.onChange} />
              <FieldError message={errors.match?.finding_types?.message} />
            </LabeledField>
          )}
        />
      </Section>
      <Section title="Decision" icon={<ScanText className="h-3.5 w-3.5" />}>
        <FieldRow>
          <LabeledField label="Decision type" className="w-56">
            <Select aria-label="Decision type" {...register("decide.type")}>
              <option value="allow">allow</option>
              <option value="deny">deny</option>
              <option value="redact">redact</option>
              <option value="quarantine">quarantine</option>
              <option value="require_human">require_human</option>
            </Select>
            <FieldError message={errors.decide?.type?.message} />
          </LabeledField>
          <LabeledField label="Reason" description="Logged with the decision." className="flex-1">
            <Input
              type="text"
              placeholder="Why this decision applies"
              aria-label="Decision reason"
              {...register("decide.reason")}
            />
            <FieldError message={errors.decide?.reason?.message} />
          </LabeledField>
        </FieldRow>
        <FieldRow>
          <LabeledField label="Redact strategy" description="Optional hint for the redactor." className="flex-1">
            <Input
              type="text"
              placeholder="e.g. mask, tokenize"
              aria-label="Redact strategy"
              {...register("decide.redact_strategy")}
            />
            <FieldError message={errors.decide?.redact_strategy?.message} />
          </LabeledField>
        </FieldRow>
      </Section>
    </FormShell>
  );
}

function VelocityRuleForm({ rule, onChange }: PerTypeFormProps<typeof RuleType.velocity>) {
  const defaultValues = useMemo(
    () => ruleToFormValues(rule) as VelocityRuleFormValues,
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );
  const form = useForm<VelocityRuleFormValues>({
    resolver: zodResolver(velocityRuleFormSchema),
    defaultValues,
    mode: "onChange",
  });
  useRuleFormSync({ form, schema: velocityRuleFormSchema, rule, onChange });

  const {
    register,
    control,
    formState: { errors },
  } = form;

  return (
    <FormShell label="Velocity rule editor">
      <EnvelopeFields register={register} control={control} errors={errors} />
      <Section title="Match selectors" icon={<Filter className="h-3.5 w-3.5" />}>
        <ArrayField
          name="match.tenants"
          label="Tenants"
          description="Tenant ids this rule applies to (empty = all)."
          control={form.control as unknown as Control<RuleFormValues>}
          error={errors.match?.tenants?.message}
        />
        <ArrayField
          name="match.topics"
          label="Topics"
          description="Topics this rule applies to (empty = all)."
          control={form.control as unknown as Control<RuleFormValues>}
          error={errors.match?.topics?.message}
        />
        <ArrayField
          name="match.risk_tags"
          label="Risk tags"
          description="Required risk tags on the job context."
          control={form.control as unknown as Control<RuleFormValues>}
          error={errors.match?.risk_tags?.message}
        />
      </Section>
      <Section title="Throttle" icon={<GaugeCircle className="h-3.5 w-3.5" />}>
        <FieldRow>
          <LabeledField label="Per minute" className="w-32">
            <Input
              type="number"
              min={1}
              placeholder="—"
              aria-label="Max per minute"
              {...register("decide.max_per_minute", { valueAsNumber: true, setValueAs: numberOrUndefined })}
            />
            <FieldError message={errors.decide?.max_per_minute?.message} />
          </LabeledField>
          <LabeledField label="Per hour" className="w-32">
            <Input
              type="number"
              min={1}
              placeholder="—"
              aria-label="Max per hour"
              {...register("decide.max_per_hour", { valueAsNumber: true, setValueAs: numberOrUndefined })}
            />
            <FieldError message={errors.decide?.max_per_hour?.message} />
          </LabeledField>
          <LabeledField label="Per day" className="w-32">
            <Input
              type="number"
              min={1}
              placeholder="—"
              aria-label="Max per day"
              {...register("decide.max_per_day", { valueAsNumber: true, setValueAs: numberOrUndefined })}
            />
            <FieldError message={errors.decide?.max_per_day?.message} />
          </LabeledField>
          <LabeledField label="Burst limit" className="w-32">
            <Input
              type="number"
              min={1}
              placeholder="—"
              aria-label="Burst limit"
              {...register("decide.burst_limit", { valueAsNumber: true, setValueAs: numberOrUndefined })}
            />
            <FieldError message={errors.decide?.burst_limit?.message} />
          </LabeledField>
        </FieldRow>
        <FieldRow>
          <LabeledField label="Reason" description="Logged with the decision." className="flex-1">
            <Input
              type="text"
              placeholder="Why this throttle applies"
              aria-label="Decision reason"
              {...register("decide.reason")}
            />
            <FieldError message={errors.decide?.reason?.message} />
          </LabeledField>
        </FieldRow>
      </Section>
    </FormShell>
  );
}

function EdgeRuleForm({ rule, onChange }: PerTypeFormProps<typeof RuleType.edge>) {
  const defaultValues = useMemo(
    () => ruleToFormValues(rule) as EdgeRuleFormValues,
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );
  const form = useForm<EdgeRuleFormValues>({
    resolver: zodResolver(edgeRuleFormSchema),
    defaultValues,
    mode: "onChange",
  });
  useRuleFormSync({ form, schema: edgeRuleFormSchema, rule, onChange });

  const {
    register,
    control,
    formState: { errors },
  } = form;

  return (
    <FormShell label="Edge rule editor">
      <EnvelopeFields register={register} control={control} errors={errors} />
      <Section title="Match selectors" icon={<Filter className="h-3.5 w-3.5" />}>
        <ArrayField
          name="match.tools"
          label="Tools"
          description="Edge tool names (e.g. Bash, Edit, Read)."
          control={form.control as unknown as Control<RuleFormValues>}
          error={errors.match?.tools?.message}
        />
        <FieldRow>
          <LabeledField
            label="Command pattern"
            description="Optional regex applied to the command text."
            className="flex-1"
          >
            <Input
              type="text"
              placeholder="e.g. ^rm\\s+-rf"
              aria-label="Command pattern"
              {...register("match.command_pattern")}
            />
            <FieldError message={errors.match?.command_pattern?.message} />
          </LabeledField>
        </FieldRow>
        <FieldRow>
          <LabeledField
            label="Path pattern"
            description="Optional regex applied to the affected file path."
            className="flex-1"
          >
            <Input
              type="text"
              placeholder="e.g. ^/etc/"
              aria-label="Path pattern"
              {...register("match.path_pattern")}
            />
            <FieldError message={errors.match?.path_pattern?.message} />
          </LabeledField>
        </FieldRow>
        <ArrayField
          name="match.risk_tags"
          label="Risk tags"
          description="Required risk tags on the edge context."
          control={form.control as unknown as Control<RuleFormValues>}
          error={errors.match?.risk_tags?.message}
        />
      </Section>
      <Section title="Decision" icon={<Slash className="h-3.5 w-3.5" />}>
        <FieldRow>
          <LabeledField label="Decision type" className="w-56">
            <Select aria-label="Decision type" {...register("decide.type")}>
              <option value="allow">allow</option>
              <option value="deny">deny</option>
              <option value="require_human">require_human</option>
              <option value="allow_with_constraints">allow_with_constraints</option>
            </Select>
            <FieldError message={errors.decide?.type?.message} />
          </LabeledField>
          <LabeledField label="Reason" description="Logged with the decision." className="flex-1">
            <Input
              type="text"
              placeholder="Why this decision applies"
              aria-label="Decision reason"
              {...register("decide.reason")}
            />
            <FieldError message={errors.decide?.reason?.message} />
          </LabeledField>
        </FieldRow>
      </Section>
    </FormShell>
  );
}

// ---------------------------------------------------------------------------
// Tiny presentational helpers (single-consumer; co-located).
// ---------------------------------------------------------------------------

function FormShell({ label, children }: { label: string; children: ReactNode }) {
  return (
    <form
      aria-label={label}
      onSubmit={(event) => event.preventDefault()}
      className="flex flex-col gap-4 overflow-y-auto pr-1"
    >
      {children}
    </form>
  );
}

function Section({ title, icon, children }: { title: string; icon: ReactNode; children: ReactNode }) {
  return (
    <fieldset className="rounded-2xl border border-border bg-surface-1/60 p-3">
      <legend className="-ml-1 px-1 text-xs font-mono uppercase tracking-widest text-muted-foreground">
        <span className="inline-flex items-center gap-1.5">
          {icon}
          {title}
        </span>
      </legend>
      <div className="space-y-3">{children}</div>
    </fieldset>
  );
}

function FieldRow({ children }: { children: ReactNode }) {
  return <div className="flex flex-wrap items-end gap-3">{children}</div>;
}

function FieldError({ message }: { message?: string }) {
  if (!message) return null;
  return (
    <p role="alert" className="mt-1 text-xs text-destructive">
      {message}
    </p>
  );
}

interface ArrayFieldProps {
  name: Path<RuleFormValues>;
  label: string;
  description: string;
  control: Control<RuleFormValues>;
  error?: string;
}

function ArrayField({ name, label, description, control, error }: ArrayFieldProps) {
  return (
    <FieldRow>
      <LabeledField
        label={
          <span className="inline-flex items-center gap-1.5">
            <Hash aria-hidden className="h-3 w-3 text-muted-foreground/70" /> {label}
          </span>
        }
        description={description}
        className="flex-1"
      >
        <Controller
          control={control}
          name={name}
          render={({ field }) => (
            <TokenInput
              value={(field.value as string[] | undefined) ?? []}
              onChange={(next) => field.onChange(next)}
              placeholder={`Add ${label.toLowerCase()}…`}
              ariaLabel={label}
              ariaInvalid={Boolean(error)}
            />
          )}
        />
        <FieldError message={error} />
      </LabeledField>
    </FieldRow>
  );
}

const FINDING_TYPES: ReadonlyArray<{ value: "secret_leak" | "pii" | "injection"; label: string }> = [
  { value: "secret_leak", label: "Secret leaks" },
  { value: "pii", label: "PII" },
  { value: "injection", label: "Prompt injection" },
];

function FindingTypeChecklist({
  value,
  onChange,
}: {
  value: ReadonlyArray<"secret_leak" | "pii" | "injection">;
  onChange: (next: Array<"secret_leak" | "pii" | "injection">) => void;
}) {
  const set = new Set(value);
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-2xl border border-border bg-surface-2/40 px-3 py-2">
      {FINDING_TYPES.map((option) => {
        const checked = set.has(option.value);
        return (
          <label
            key={option.value}
            className="inline-flex items-center gap-2 text-sm text-foreground"
          >
            <Checkbox
              checked={checked}
              onChange={(event) => {
                const isChecked = event.target.checked;
                const next = new Set(value);
                if (isChecked) next.add(option.value);
                else next.delete(option.value);
                onChange(Array.from(next));
              }}
              aria-label={option.label}
            />
            <span>{option.label}</span>
          </label>
        );
      })}
    </div>
  );
}

// Coerce numeric input values: an empty string from <input type="number" />
// must become `undefined` (not NaN) so the Zod schema's optional() branch
// triggers instead of an "Expected number" error.
function numberOrUndefined(raw: unknown): number | undefined {
  if (raw === "" || raw === null || raw === undefined) return undefined;
  const num = typeof raw === "number" ? raw : Number(raw);
  return Number.isFinite(num) ? num : undefined;
}
