import { useEffect, useId, useState } from "react";
import { Plus, Trash2, X } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { cn } from "@/lib/utils";
import { isValidGlobalRuleIdSlug } from "@/lib/policy-studio/globalPolicy";
import { PolicyAdvancedToggle } from "@/components/policy/studio-primitives/PolicyAdvancedToggle";
import { PolicyDecisionSelect } from "@/components/policy/studio-primitives/PolicyDecisionSelect";
import { PolicyEmptyConfigCard } from "@/components/policy/studio-primitives/PolicyEmptyConfigCard";
import { PolicyField } from "@/components/policy/studio-primitives/PolicyField";
import { PolicySection } from "@/components/policy/studio-primitives/PolicySection";
import { PolicyTagInput } from "@/components/policy/studio-primitives/PolicyTagInput";
import { useDialogA11y } from "@/hooks/useDialogA11y";
import { usePolicyStudioTelemetry } from "@/hooks/usePolicyStudioTelemetry";
import { getAdvancedConfiguredSummary } from "@/lib/policy-studio/globalRuleEditorState";
import type { GlobalPolicyInputRule, GlobalPolicyRemediation } from "@/types/policy";

interface GlobalRuleEditorDrawerProps {
  open: boolean;
  rule?: GlobalPolicyInputRule | null;
  nextRuleIndex?: number;
  existingRuleIds?: string[];
  onClose: () => void;
  onSave: (rule: GlobalPolicyInputRule) => void;
}

interface GlobalRuleValidationErrors {
  ruleId?: string;
  decision?: string;
  reason?: string;
  labels?: string;
  maxRuntimeMs?: string;
  maxConcurrentJobs?: string;
  remediations?: string;
}

interface ValidateGlobalRuleDraftInput {
  draft: GlobalPolicyInputRule;
  existingRuleIds: string[];
  labelLineErrors: string[];
  maxRuntimeMsInput: string;
  maxConcurrentJobsInput: string;
}

function labelsToText(labels: Record<string, string>): string {
  return Object.entries(labels).map(([k, v]) => `${k}=${v}`).join("\n");
}

interface ParsedLabelsResult {
  labels: Record<string, string>;
  errors: string[];
}

const LABEL_KEY_PATTERN = /^[a-zA-Z0-9_.-]+$/;

function parseLabelsText(raw: string): ParsedLabelsResult {
  const labels: Record<string, string> = {};
  const errors: string[] = [];
  const seenKeys = new Set<string>();

  for (const [index, line] of raw.split("\n").entries()) {
    const trimmedLine = line.trim();
    if (!trimmedLine) continue;
    const separatorIndex = trimmedLine.indexOf("=");
    if (separatorIndex <= 0 || separatorIndex === trimmedLine.length - 1) {
      errors.push(`Line ${index + 1}: expected key=value format.`);
      continue;
    }
    const k = trimmedLine.slice(0, separatorIndex);
    const rest = [trimmedLine.slice(separatorIndex + 1)];
    const key = (k ?? "").trim();
    const value = rest.join("=").trim();
    if (!LABEL_KEY_PATTERN.test(key)) {
      errors.push(`Line ${index + 1}: label key "${key}" contains invalid characters.`);
      continue;
    }
    if (seenKeys.has(key)) {
      errors.push(`Line ${index + 1}: duplicate label key "${key}".`);
      continue;
    }
    seenKeys.add(key);
    labels[key] = value;
  }
  return { labels, errors };
}

function parseOptionalNumber(raw: string): number | undefined {
  if (!raw.trim()) return undefined;
  const parsed = Number(raw);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function numberToInput(value?: number): string {
  return typeof value === "number" && Number.isFinite(value) ? String(value) : "";
}

function validatePositiveIntegerInput(raw: string, fieldLabel: string): string | undefined {
  const value = raw.trim();
  if (!value) return undefined;
  if (!/^\d+$/.test(value)) {
    return `${fieldLabel} must be a whole number.`;
  }
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return `${fieldLabel} must be greater than 0.`;
  }
  return undefined;
}

function isRemediationActionable(remediation: GlobalPolicyRemediation): boolean {
  return Boolean(
    remediation.replacementTopic.trim()
      || remediation.replacementCapability.trim()
      || Object.keys(remediation.addLabels).length > 0
      || remediation.removeLabels.length > 0,
  );
}

function areConstraintsEnabledForDecision(
  decision: GlobalPolicyInputRule["decision"],
): boolean {
  return decision === "allow_with_constraints" || decision === "throttle";
}

function hasRemediationDecisionMismatch(
  decision: GlobalPolicyInputRule["decision"],
  remediations: GlobalPolicyRemediation[],
): boolean {
  return decision !== "deny" && remediations.length > 0;
}

function validateGlobalRuleDraft({
  draft,
  existingRuleIds,
  labelLineErrors,
  maxRuntimeMsInput,
  maxConcurrentJobsInput,
}: ValidateGlobalRuleDraftInput): GlobalRuleValidationErrors {
  const errors: GlobalRuleValidationErrors = {};
  const trimmedRuleId = draft.id.trim();
  if (!trimmedRuleId) {
    errors.ruleId = "Rule ID is required.";
  } else if (!isValidGlobalRuleIdSlug(trimmedRuleId)) {
    errors.ruleId = "Rule ID must be slug-formatted (lowercase letters, numbers, dashes).";
  } else if (existingRuleIds.some((existing) => existing.trim().toLowerCase() === trimmedRuleId.toLowerCase())) {
    errors.ruleId = "Rule ID must be unique within the bundle.";
  }

  if (!draft.decision?.trim()) {
    errors.decision = "Decision is required.";
  }

  const decisionNeedsReason = draft.decision === "deny" || draft.decision === "require_approval";
  if (decisionNeedsReason && !draft.reason.trim()) {
    errors.reason = "Reason is required for deny and require_approval decisions.";
  }

  if (labelLineErrors.length > 0) {
    errors.labels = `Fix ${labelLineErrors.length} invalid label line(s).`;
  }

  errors.maxRuntimeMs = validatePositiveIntegerInput(maxRuntimeMsInput, "max_runtime_ms");
  errors.maxConcurrentJobs = validatePositiveIntegerInput(
    maxConcurrentJobsInput,
    "max_concurrent_jobs",
  );

  const invalidRemediationIndex = draft.remediations.findIndex((remediation) => {
    if (!remediation.id.trim() || !remediation.title.trim()) return true;
    return !isRemediationActionable(remediation);
  });
  if (invalidRemediationIndex >= 0) {
    errors.remediations = `Remediation ${invalidRemediationIndex + 1} requires id, title, and at least one actionable target.`;
  }

  return errors;
}

function newRemediation(index: number): GlobalPolicyRemediation {
  return {
    id: `remediation-${index + 1}`,
    title: "",
    summary: "",
    replacementTopic: "",
    replacementCapability: "",
    addLabels: {},
    removeLabels: [],
    source: {},
  };
}

export function createEmptyGlobalInputRule(nextIndex = 1): GlobalPolicyInputRule {
  return {
    id: `rule-${nextIndex}`,
    decision: "deny",
    reason: "",
    match: {
      tenants: [],
      topics: [],
      capabilities: [],
      riskTags: [],
      requires: [],
      packIds: [],
      actorIds: [],
      actorTypes: [],
      labels: {},
      secretsPresent: null,
      mcp: {
        allowServers: [],
        denyServers: [],
        allowTools: [],
        denyTools: [],
        allowResources: [],
        denyResources: [],
        allowActions: [],
        denyActions: [],
      },
    },
    constraints: {
      budgets: {},
      sandbox: { networkAllowlist: [], fsReadOnly: [], fsReadWrite: [] },
      toolchain: { allowedTools: [], allowedCommands: [] },
      diff: { denyPathGlobs: [] },
    },
    remediations: [],
    source: {},
  };
}

export function GlobalRuleEditorDrawer({
  open,
  rule,
  nextRuleIndex = 1,
  existingRuleIds = [],
  onClose,
  onSave,
}: GlobalRuleEditorDrawerProps) {
  const [draft, setDraft] = useState<GlobalPolicyInputRule>(() =>
    rule ? structuredClone(rule) : createEmptyGlobalInputRule(nextRuleIndex),
  );
  const [labelsText, setLabelsText] = useState("");
  const [labelLineErrors, setLabelLineErrors] = useState<string[]>([]);
  const [maxRuntimeMsInput, setMaxRuntimeMsInput] = useState("");
  const [maxConcurrentJobsInput, setMaxConcurrentJobsInput] = useState("");
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [showRemediationMismatchConfirm, setShowRemediationMismatchConfirm] = useState(false);
  const [viewedSections, setViewedSections] = useState({
    advanced: false,
    constraints: false,
    remediations: false,
  });
  const [error, setError] = useState("");
  const dialogRef = useDialogA11y(onClose, { enabled: open });
  const dialogTitleId = useId();
  const validationSummaryId = "global-rule-validation-summary";
  const { emit: emitTelemetry } = usePolicyStudioTelemetry();
  const constraintsEnabled = areConstraintsEnabledForDecision(draft.decision);
  const decisionNeedsReason =
    draft.decision === "deny" || draft.decision === "require_approval";

  const validationErrors: GlobalRuleValidationErrors = validateGlobalRuleDraft({
    draft,
    existingRuleIds,
    labelLineErrors,
    maxRuntimeMsInput,
    maxConcurrentJobsInput,
  });
  const hasValidationErrors = Object.values(validationErrors).some(Boolean);

  useEffect(() => {
    if (!open) return;
    const next = rule ? structuredClone(rule) : createEmptyGlobalInputRule(nextRuleIndex);
    const nextLabelsText = labelsToText(next.match.labels);
    const parsedLabels = parseLabelsText(nextLabelsText);
    setDraft(next);
    setLabelsText(nextLabelsText);
    setLabelLineErrors(parsedLabels.errors);
    setMaxRuntimeMsInput(numberToInput(next.constraints.budgets.maxRuntimeMs));
    setMaxConcurrentJobsInput(numberToInput(next.constraints.budgets.maxConcurrentJobs));
    setAdvancedOpen(false);
    setShowRemediationMismatchConfirm(false);
    setViewedSections({ advanced: false, constraints: false, remediations: false });
    setError("");
  }, [nextRuleIndex, open, rule]);

  useEffect(() => {
    if (!open) return;

    function handleEscape(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
      }
    }

    document.addEventListener("keydown", handleEscape);
    return () => document.removeEventListener("keydown", handleEscape);
  }, [onClose, open]);

  useEffect(() => {
    if (!hasValidationErrors && error) {
      setError("");
    }
  }, [error, hasValidationErrors]);

  useEffect(() => {
    if (
      !hasRemediationDecisionMismatch(draft.decision, draft.remediations)
      && showRemediationMismatchConfirm
    ) {
      setShowRemediationMismatchConfirm(false);
    }
  }, [draft.decision, draft.remediations, showRemediationMismatchConfirm]);

  const firstInvalidFieldId = validationErrors.ruleId
    ? "global-rule-id"
    : validationErrors.decision
      ? "global-rule-decision"
      : validationErrors.reason
        ? "global-rule-reason"
        : validationErrors.labels
          ? "global-rule-labels"
          : validationErrors.maxRuntimeMs
            ? "global-rule-max-runtime-ms"
            : validationErrors.maxConcurrentJobs
              ? "global-rule-max-concurrent-jobs"
              : validationErrors.remediations
                ? "global-rule-remediation-id-0"
                : null;
  const showPrimaryConstraints = draft.decision === "allow_with_constraints";
  const showPrimaryRemediations = draft.decision === "deny";
  const showAdvancedRemediations = advancedOpen && draft.decision === "require_approval";
  const showThrottleHint = draft.decision === "throttle" && !advancedOpen;
  const showAdvancedSection = advancedOpen;
  const showConstraintsSection = showPrimaryConstraints || (advancedOpen && draft.decision === "throttle");
  const showRemediationsSection = showPrimaryRemediations || showAdvancedRemediations;
  const advancedConfigured = getAdvancedConfiguredSummary(draft);
  const remediationDecisionMismatch = hasRemediationDecisionMismatch(
    draft.decision,
    draft.remediations,
  );
  const hasConstraintConfig = Boolean(
    maxRuntimeMsInput.trim()
      || maxConcurrentJobsInput.trim()
      || draft.constraints.toolchain.allowedTools.length
      || draft.constraints.diff.denyPathGlobs.length,
  );
  const handleAdvancedToggle = (nextOpen: boolean) => {
    setAdvancedOpen(nextOpen);
    emitTelemetry("policy_editor_advanced_toggled", {
      scope: "input_global",
      advancedOpen: nextOpen,
      configuredAdvancedCount: advancedConfigured.count,
      decision: draft.decision,
    });
  };
  const commitSave = (clearRemediations: boolean) => {
    const id = draft.id.trim();
    const payload: GlobalPolicyInputRule = clearRemediations
      ? { ...draft, id, remediations: [] }
      : { ...draft, id };
    if (advancedConfigured.count > 0) {
      emitTelemetry("policy_editor_saved_with_advanced_fields", {
        scope: "input_global",
        configuredAdvancedCount: advancedConfigured.count,
        decision: draft.decision,
        clearRemediationsOnSave: clearRemediations,
      });
      if (!viewedSections.advanced) {
        emitTelemetry("policy_editor_saved_with_hidden_advanced_unviewed", {
          scope: "input_global",
          configuredAdvancedCount: advancedConfigured.count,
          decision: draft.decision,
          clearRemediationsOnSave: clearRemediations,
        });
      }
    }
    onSave(payload);
    setShowRemediationMismatchConfirm(false);
  };

  useEffect(() => {
    if (!open || !advancedOpen || viewedSections.advanced) return;
    setViewedSections((previous) => ({ ...previous, advanced: true }));
    emitTelemetry("policy_editor_section_viewed", {
      scope: "input_global",
      section: "advanced",
      decision: draft.decision,
      configuredAdvancedCount: advancedConfigured.count,
    });
  }, [advancedConfigured.count, advancedOpen, draft.decision, emitTelemetry, viewedSections.advanced]);

  useEffect(() => {
    if (!open || !showConstraintsSection || viewedSections.constraints) return;
    setViewedSections((previous) => ({ ...previous, constraints: true }));
    emitTelemetry("policy_editor_section_viewed", {
      scope: "input_global",
      section: "constraints",
      decision: draft.decision,
      configuredAdvancedCount: advancedConfigured.count,
    });
  }, [advancedConfigured.count, draft.decision, emitTelemetry, showConstraintsSection, viewedSections.constraints]);

  useEffect(() => {
    if (!open || !showRemediationsSection || viewedSections.remediations) return;
    setViewedSections((previous) => ({ ...previous, remediations: true }));
    emitTelemetry("policy_editor_section_viewed", {
      scope: "input_global",
      section: "remediations",
      decision: draft.decision,
      configuredAdvancedCount: advancedConfigured.count,
    });
  }, [advancedConfigured.count, draft.decision, emitTelemetry, showRemediationsSection, viewedSections.remediations]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-[130] flex justify-end">
      <button
        type="button"
        className="absolute inset-0 bg-black/50"
        aria-label="Close editor"
        onClick={onClose}
      />
      <div
        ref={dialogRef}
        className="relative h-full w-full max-w-xl overflow-y-auto border-l border-border bg-surface-1 p-5"
        role="dialog"
        aria-modal="true"
        aria-labelledby={dialogTitleId}
      >
        <div className="mb-4 flex items-center justify-between">
          <h2 className="font-display text-lg font-semibold text-foreground" id={dialogTitleId}>
            {rule ? "Edit Rule" : "New Rule"}
          </h2>
          <button
            type="button"
            className="rounded-xl p-2 text-muted-foreground hover:bg-surface-2"
            onClick={onClose}
            aria-label="Close editor"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="space-y-4 pb-20">
          <PolicyField
            inputId="global-rule-id"
            label="Rule ID"
            required
            error={validationErrors.ruleId}
            helpText="Unique identifier for this input rule. Use lowercase letters, numbers, and dashes to keep IDs stable across edits."
            hint="Example: deny-untrusted-topic"
          >
            <input
              autoFocus
              className="mt-1 h-8 w-full rounded-xl border border-border bg-surface-2 px-3 text-xs text-foreground"
              value={draft.id}
              onChange={(e) => setDraft((p) => ({ ...p, id: e.target.value }))}
            />
          </PolicyField>

          <PolicyDecisionSelect
            inputId="global-rule-decision"
            value={draft.decision}
            onChange={(next) => setDraft((p) => ({ ...p, decision: next }))}
            error={validationErrors.decision}
          />

          {showPrimaryConstraints && (
            <p className="rounded-xl border border-cordum/30 bg-cordum/10 px-3 py-2 text-xs text-cordum-foreground">
              <strong>allow_with_constraints:</strong> configure constraints below to enforce runtime/tooling boundaries.
            </p>
          )}
          {showPrimaryRemediations && (
            <p className="rounded-xl border border-cordum/30 bg-cordum/10 px-3 py-2 text-xs text-cordum-foreground">
              <strong>deny:</strong> add remediations below so operators have safe alternatives.
            </p>
          )}
          {showThrottleHint && (
            <p className="rounded-xl border border-cordum/30 bg-cordum/10 px-3 py-2 text-xs text-cordum-foreground">
              <strong>throttle:</strong> optional constraints are available in Advanced.
            </p>
          )}
          {draft.decision === "require_approval" && !advancedOpen && (
            <p className="rounded-xl border border-cordum/30 bg-cordum/10 px-3 py-2 text-xs text-cordum-foreground">
              Remediations for require_approval are available in Advanced.
            </p>
          )}

          <PolicyField
            inputId="global-rule-reason"
            label="Reason"
            required={decisionNeedsReason}
            error={validationErrors.reason}
            helpText="Human-readable explanation shown during audits and simulation results."
            hint='Use clear action-oriented text (e.g., "Block privileged MCP tool access for untrusted actors").'
          >
            <textarea
              rows={2}
              className="mt-1 w-full rounded-xl border border-border bg-surface-2 px-3 py-2 text-xs text-foreground"
              value={draft.reason}
              onChange={(e) => setDraft((p) => ({ ...p, reason: e.target.value }))}
            />
          </PolicyField>

          <PolicyTagInput
            inputId="global-rule-capabilities"
            label="Capabilities"
            helpText="Comma-separated capability IDs to match. Leave empty to match any capability."
            value={draft.match.capabilities}
            onChange={(next) => setDraft((p) => ({ ...p, match: { ...p.match, capabilities: next } }))}
          />
          <PolicyTagInput
            inputId="global-rule-topics"
            label="Topics"
            helpText="Comma-separated topic patterns this rule applies to."
            value={draft.match.topics}
            onChange={(next) => setDraft((p) => ({ ...p, match: { ...p.match, topics: next } }))}
          />
          <PolicyTagInput
            inputId="global-rule-risk-tags"
            label="Risk tags"
            helpText="Comma-separated risk tags required for this rule to match."
            value={draft.match.riskTags}
            onChange={(next) => setDraft((p) => ({ ...p, match: { ...p.match, riskTags: next } }))}
          />

          <div className="flex justify-end">
            <PolicyAdvancedToggle
              open={advancedOpen}
              onToggle={handleAdvancedToggle}
              configuredCount={advancedConfigured.count}
            />
          </div>

          {showAdvancedSection && (
            <PolicySection
              title="Advanced match controls"
              description="Optional actor/MCP/label matching for power-user workflows."
              defaultOpen
            >
              <PolicyTagInput
                inputId="global-rule-actor-ids"
                label="Actor IDs"
                helpText="Comma-separated principal/actor IDs. Use to scope rule decisions to known identities."
                value={draft.match.actorIds}
                onChange={(next) => setDraft((p) => ({ ...p, match: { ...p.match, actorIds: next } }))}
              />
              <PolicyTagInput
                inputId="global-rule-mcp-allow-servers"
                label="Allow MCP servers"
                helpText="Comma-separated MCP server IDs explicitly allowed when this rule matches."
                hint="MCP governance is primarily configured in Tenant scope."
                value={draft.match.mcp.allowServers}
                onChange={(next) => setDraft((p) => ({ ...p, match: { ...p.match, mcp: { ...p.match.mcp, allowServers: next } } }))}
              />
              <PolicyTagInput
                inputId="global-rule-mcp-deny-tools"
                label="Deny MCP tools"
                helpText="Comma-separated MCP tool names to deny when this rule matches."
                hint="MCP governance is primarily configured in Tenant scope."
                value={draft.match.mcp.denyTools}
                onChange={(next) => setDraft((p) => ({ ...p, match: { ...p.match, mcp: { ...p.match.mcp, denyTools: next } } }))}
              />
              <p className="text-xs text-muted-foreground">
                MCP precedence: <span className="font-medium text-foreground">deny overrides allow</span> when both rules match.
              </p>

              <PolicyField
                inputId="global-rule-labels"
                label="Labels"
                error={validationErrors.labels}
                helpText="One key=value entry per line. Labels become exact-match conditions."
                hint="Format: env=prod"
              >
                <textarea
                  rows={2}
                  className="mt-1 w-full rounded-xl border border-border bg-surface-2 px-3 py-2 text-xs text-foreground"
                  value={labelsText}
                  onChange={(e) => {
                    const nextText = e.target.value;
                    const parsedLabels = parseLabelsText(nextText);
                    setLabelsText(nextText);
                    setLabelLineErrors(parsedLabels.errors);
                    setDraft((p) => ({ ...p, match: { ...p.match, labels: parsedLabels.labels } }));
                  }}
                />
              </PolicyField>
              {labelLineErrors.length > 0 && (
                <ul className="space-y-1 rounded-xl border border-destructive/30 bg-destructive/10 p-2 text-xs text-destructive">
                  {labelLineErrors.map((lineError) => (
                    <li key={lineError}>{lineError}</li>
                  ))}
                </ul>
              )}
            </PolicySection>
          )}

          {showConstraintsSection && (
            <PolicySection
              title="Constraints"
              description="Runtime and toolchain boundaries for matching requests."
              defaultOpen
            >
              <div
                className={cn(
                  "space-y-3 rounded-xl border border-border bg-surface-0 p-3",
                  !constraintsEnabled && "opacity-70",
                )}
              >
                <p className="text-xs text-muted-foreground">
                  Constraints are active for <span className="font-medium text-foreground">allow_with_constraints</span> and <span className="font-medium text-foreground">throttle</span>.
                </p>
                {!hasConstraintConfig && (
                  <PolicyEmptyConfigCard
                    title="No constraints configured"
                    description="Add at least one runtime or toolchain guard."
                    ctaLabel="Set max runtime"
                    onCtaClick={() => {
                      setMaxRuntimeMsInput("1500");
                      setDraft((p) => ({
                        ...p,
                        constraints: {
                          ...p.constraints,
                          budgets: {
                            ...p.constraints.budgets,
                            maxRuntimeMs: 1500,
                          },
                        },
                      }));
                    }}
                  />
                )}
                <div className="grid grid-cols-2 gap-3">
                  <PolicyField
                    inputId="global-rule-max-runtime-ms"
                    label="max_runtime_ms"
                    error={validationErrors.maxRuntimeMs}
                    helpText="Upper runtime budget in milliseconds when constraints are active."
                  >
                    <input
                      type="number"
                      inputMode="numeric"
                      min={0}
                      disabled={!constraintsEnabled}
                      className="mt-1 h-8 w-full rounded-xl border border-border bg-surface-2 px-3 text-xs text-foreground disabled:cursor-not-allowed disabled:opacity-70"
                      value={maxRuntimeMsInput}
                      onChange={(e) => {
                        const nextValue = e.target.value;
                        setMaxRuntimeMsInput(nextValue);
                        setDraft((p) => ({
                          ...p,
                          constraints: {
                            ...p.constraints,
                            budgets: {
                              ...p.constraints.budgets,
                              maxRuntimeMs: parseOptionalNumber(nextValue),
                            },
                          },
                        }));
                      }}
                    />
                  </PolicyField>
                  <PolicyField
                    inputId="global-rule-max-concurrent-jobs"
                    label="max_concurrent_jobs"
                    error={validationErrors.maxConcurrentJobs}
                    helpText="Maximum concurrent jobs allowed when this rule decision is in effect."
                  >
                    <input
                      type="number"
                      inputMode="numeric"
                      min={0}
                      disabled={!constraintsEnabled}
                      className="mt-1 h-8 w-full rounded-xl border border-border bg-surface-2 px-3 text-xs text-foreground disabled:cursor-not-allowed disabled:opacity-70"
                      value={maxConcurrentJobsInput}
                      onChange={(e) => {
                        const nextValue = e.target.value;
                        setMaxConcurrentJobsInput(nextValue);
                        setDraft((p) => ({
                          ...p,
                          constraints: {
                            ...p.constraints,
                            budgets: {
                              ...p.constraints.budgets,
                              maxConcurrentJobs: parseOptionalNumber(nextValue),
                            },
                          },
                        }));
                      }}
                    />
                  </PolicyField>
                </div>
                <PolicyTagInput
                  inputId="global-rule-allowed-tools"
                  label="toolchain.allowed_tools"
                  helpText="Comma-separated tool identifiers permitted when constraints are active."
                  disabled={!constraintsEnabled}
                  value={draft.constraints.toolchain.allowedTools}
                  onChange={(next) => setDraft((p) => ({ ...p, constraints: { ...p.constraints, toolchain: { ...p.constraints.toolchain, allowedTools: next } } }))}
                />
                <PolicyTagInput
                  inputId="global-rule-deny-path-globs"
                  label="diff.deny_path_globs"
                  helpText="Comma-separated file path globs blocked for modifications when constraints are active."
                  disabled={!constraintsEnabled}
                  value={draft.constraints.diff.denyPathGlobs}
                  onChange={(next) => setDraft((p) => ({ ...p, constraints: { ...p.constraints, diff: { ...p.constraints.diff, denyPathGlobs: next } } }))}
                />
              </div>
            </PolicySection>
          )}

          {showRemediationsSection && (
            <PolicySection
              title="Remediations"
              description="Suggested alternatives when execution is denied or requires approval."
              defaultOpen
              rightSlot={(
                <Button variant="outline" size="sm" onClick={() => setDraft((p) => ({ ...p, remediations: [...p.remediations, newRemediation(p.remediations.length)] }))}>
                  <Plus className="mr-1 h-3 w-3" />Add
                </Button>
              )}
            >
              {validationErrors.remediations && (
                <p className="rounded border border-destructive/30 bg-destructive/10 px-2 py-1 text-xs text-destructive" role="alert">
                  {validationErrors.remediations}
                </p>
              )}
              {draft.remediations.length === 0 && (
                <PolicyEmptyConfigCard
                  title="No remediations configured"
                  description="Add an operator-friendly remediation path."
                  ctaLabel="Add remediation"
                  onCtaClick={() => setDraft((p) => ({ ...p, remediations: [...p.remediations, newRemediation(p.remediations.length)] }))}
                />
              )}
              <div className="space-y-2">
                {draft.remediations.map((remediation, index) => (
                  <div key={`${remediation.id}-${index}`} className="rounded-xl border border-border bg-surface-0 p-2">
                    <div className="mb-2 flex justify-end">
                      <button
                        type="button"
                        aria-label={`Delete remediation ${index + 1}`}
                        className="rounded-xl p-1 text-destructive hover:bg-destructive/10"
                        onClick={() => setDraft((p) => ({ ...p, remediations: p.remediations.filter((_, i) => i !== index) }))}
                      >
                        <Trash2 className="h-3 w-3" />
                      </button>
                    </div>
                    <PolicyField
                      inputId={`global-rule-remediation-id-${index}`}
                      label={`Remediation ${index + 1} ID`}
                      required
                      helpText="Unique remediation identifier for this rule."
                      className="mb-1"
                    >
                      <input className="h-7 w-full rounded-xl border border-border bg-surface-2 px-2 text-xs text-foreground" placeholder="id" value={remediation.id} onChange={(e) => setDraft((p) => ({ ...p, remediations: p.remediations.map((r, i) => i === index ? { ...r, id: e.target.value } : r) }))} />
                    </PolicyField>
                    <PolicyField
                      inputId={`global-rule-remediation-title-${index}`}
                      label={`Remediation ${index + 1} title`}
                      required
                      helpText="Short action title for operators."
                      className="mb-1"
                    >
                      <input className="h-7 w-full rounded-xl border border-border bg-surface-2 px-2 text-xs text-foreground" placeholder="title" value={remediation.title} onChange={(e) => setDraft((p) => ({ ...p, remediations: p.remediations.map((r, i) => i === index ? { ...r, title: e.target.value } : r) }))} />
                    </PolicyField>
                    <PolicyField
                      inputId={`global-rule-remediation-topic-${index}`}
                      label={`Remediation ${index + 1} replacement_topic`}
                      helpText="Optional safer topic route to recommend for this remediation."
                    >
                      <input className="h-7 w-full rounded-xl border border-border bg-surface-2 px-2 text-xs text-foreground" placeholder="replacement_topic" value={remediation.replacementTopic} onChange={(e) => setDraft((p) => ({ ...p, remediations: p.remediations.map((r, i) => i === index ? { ...r, replacementTopic: e.target.value } : r) }))} />
                    </PolicyField>
                  </div>
                ))}
              </div>
            </PolicySection>
          )}
        </div>

        <div className="fixed bottom-0 right-0 z-[131] flex w-full max-w-xl flex-col gap-2 border-t border-border bg-surface-1 px-5 py-3">
          {showRemediationMismatchConfirm && (
            <div className="rounded-xl border border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10 p-2 text-xs text-[var(--color-warning)]">
              <p>
                This rule has remediations configured but decision is{" "}
                <span className="font-mono text-[var(--color-warning)]">{draft.decision}</span>. Remediations apply to deny decisions.
              </p>
              <div className="mt-2 flex flex-wrap gap-2">
                <Button size="sm" onClick={() => commitSave(false)}>
                  Keep remediations (Recommended)
                </Button>
                <Button variant="outline" size="sm" onClick={() => commitSave(true)}>
                  Clear remediations now
                </Button>
              </div>
            </div>
          )}
          <div className="flex items-center justify-between">
            <div className="space-y-1">
              {hasValidationErrors && (
                <p id={validationSummaryId} className="text-xs text-[var(--color-warning)]" aria-live="polite">
                  Fix highlighted validation errors before saving.
                </p>
              )}
              <span className="text-xs text-destructive" role="alert" aria-live="assertive">{error}</span>
            </div>
            <div className="flex gap-2">
              <Button variant="outline" size="sm" onClick={onClose}>Cancel</Button>
              <Button
                size="sm"
                disabled={hasValidationErrors}
                aria-describedby={hasValidationErrors ? validationSummaryId : undefined}
                onClick={() => {
                  if (hasValidationErrors) {
                    setError("Fix validation errors before saving this rule.");
                    if (firstInvalidFieldId) {
                      document.getElementById(firstInvalidFieldId)?.focus();
                    }
                    return;
                  }
                  if (remediationDecisionMismatch) {
                    setShowRemediationMismatchConfirm(true);
                    return;
                  }
                  commitSave(false);
                }}
              >
                Save rule
              </Button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

export const __globalRuleEditorDrawerInternal = {
  parseLabelsText,
  validatePositiveIntegerInput,
  isRemediationActionable,
  areConstraintsEnabledForDecision,
  hasRemediationDecisionMismatch,
  validateGlobalRuleDraft,
};

