import { useState, useCallback, useRef, useEffect } from "react";
import { Link } from "react-router-dom";
import { Plus, Trash2, Zap, Eye, EyeOff, AlertTriangle, ArrowRight, Pencil } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Input } from "../ui/Input";
import { useSimulatePolicy, useExplainPolicy, type SimulateResult } from "../../hooks/usePolicies";
import { ExplainResultPanel } from "./ExplainResult";
import { cn } from "../../lib/utils";
import { useAuth } from "../../hooks/useAuth";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface MetaRow {
  key: string;
  value: string;
}

interface RuleEvaluation {
  ruleId: string;
  decision: string;
  reason: string;
  matched: boolean;
}

// ---------------------------------------------------------------------------
// Decision styling
// ---------------------------------------------------------------------------

const decisionColor: Record<string, string> = {
  allow: "border-success bg-[color:rgba(31,122,87,0.08)]",
  deny: "border-danger bg-[color:rgba(184,58,58,0.08)]",
  require_approval: "border-warning bg-[color:rgba(197,138,28,0.08)]",
  throttle: "border-info bg-[color:rgba(59,130,246,0.08)]",
};

const decisionBadge: Record<string, "success" | "governance" | "warning" | "info" | "default"> = {
  allow: "success",
  deny: "governance",
  require_approval: "warning",
  throttle: "info",
};

// ---------------------------------------------------------------------------
// Reusable tag input
// ---------------------------------------------------------------------------

function TagInput({
  label,
  placeholder,
  values,
  onChange,
}: {
  label: string;
  placeholder: string;
  values: string[];
  onChange: (vals: string[]) => void;
}) {
  const [draft, setDraft] = useState("");

  const add = useCallback(() => {
    const trimmed = draft.trim();
    if (trimmed && !values.includes(trimmed)) {
      onChange([...values, trimmed]);
    }
    setDraft("");
  }, [draft, values, onChange]);

  return (
    <div>
      <label className="mb-1 block text-xs font-semibold text-muted-foreground">
        {label}
      </label>
      <div className="flex gap-2">
        <Input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
          placeholder={placeholder}
          className="flex-1"
        />
        <Button variant="outline" size="sm" type="button" onClick={add}>
          <Plus className="h-3.5 w-3.5" />
        </Button>
      </div>
      {values.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {values.map((v) => (
            <button
              key={v}
              type="button"
              onClick={() => onChange(values.filter((x) => x !== v))}
              className="inline-flex items-center gap-1 rounded-full border border-border px-2.5 py-0.5 text-xs font-medium text-ink transition hover:border-danger hover:text-danger"
            >
              {v}
              <Trash2 className="h-3 w-3" />
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Metadata key-value editor
// ---------------------------------------------------------------------------

function MetadataEditor({
  rows,
  onChange,
}: {
  rows: MetaRow[];
  onChange: (rows: MetaRow[]) => void;
}) {
  const addRow = useCallback(() => {
    onChange([...rows, { key: "", value: "" }]);
  }, [rows, onChange]);

  const updateRow = useCallback(
    (idx: number, field: "key" | "value", val: string) => {
      const next = rows.map((r, i) =>
        i === idx ? { ...r, [field]: val } : r,
      );
      onChange(next);
    },
    [rows, onChange],
  );

  const removeRow = useCallback(
    (idx: number) => {
      onChange(rows.filter((_, i) => i !== idx));
    },
    [rows, onChange],
  );

  return (
    <div>
      <div className="mb-1 flex items-center justify-between">
        <label className="text-xs font-semibold text-muted-foreground">Metadata</label>
        <Button variant="ghost" size="sm" type="button" onClick={addRow}>
          <Plus className="h-3.5 w-3.5" />
          Add
        </Button>
      </div>
      {rows.length === 0 && (
        <p className="text-xs text-muted-foreground">No metadata entries.</p>
      )}
      <div className="space-y-2">
        {rows.map((row, i) => (
          <div key={i} className="flex items-center gap-2">
            <Input
              value={row.key}
              onChange={(e) => updateRow(i, "key", e.target.value)}
              placeholder="Key"
              className="flex-1"
            />
            <Input
              value={row.value}
              onChange={(e) => updateRow(i, "value", e.target.value)}
              placeholder="Value"
              className="flex-1"
            />
            <Button
              variant="ghost"
              size="sm"
              type="button"
              onClick={() => removeRow(i)}
              className="text-danger"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
        ))}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Deny Suggestion Card
// ---------------------------------------------------------------------------

function DenySuggestionCard({
  result,
  capabilities,
  riskTags,
}: {
  result: SimulateResult;
  capabilities: string[];
  riskTags: string[];
}) {
  if (result.decision !== "deny") return null;

  const capsList = capabilities.length > 0 ? capabilities.join(", ") : null;
  const tagsList = riskTags.length > 0 ? riskTags.join(", ") : null;

  return (
    <Card className="border-2 border-warning/40 bg-warning/5">
      <div className="space-y-3">
        <div className="flex items-start gap-2">
          <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-warning" />
          <div className="space-y-1">
            <p className="text-sm font-semibold text-ink">
              This request was denied
              {result.matchedRule ? (
                <> by rule <span className="font-mono text-danger">{result.matchedRule}</span></>
              ) : null}
            </p>
            {result.reason && (
              <p className="text-xs text-muted-foreground">Reason: {result.reason}</p>
            )}
          </div>
        </div>

        <div className="rounded-xl border border-border bg-surface2/30 px-3 py-2">
          <p className="text-xs text-muted-foreground">
            <span className="font-semibold text-ink">Suggestion: </span>
            {capsList && (
              <>Consider adding an exception for capabilities <span className="font-mono font-semibold">{capsList}</span></>
            )}
            {capsList && tagsList && " or "}
            {tagsList && (
              <>adjusting risk tag requirements for <span className="font-mono font-semibold">{tagsList}</span></>
            )}
            {!capsList && !tagsList && "Consider reviewing the matching rule's conditions."}
          </p>
        </div>

        {result.matchedRule && (
          <Link
            to={`/govern/overview?tab=input-rules&highlight=${encodeURIComponent(result.matchedRule)}`}
            className="inline-flex items-center gap-1.5 text-xs font-semibold text-accent hover:underline"
          >
            <Pencil className="h-3.5 w-3.5" />
            Edit Rule
          </Link>
        )}
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Evaluation waterfall (with what-if + scroll target IDs)
// ---------------------------------------------------------------------------

function EvaluationWaterfall({
  rules,
  highlightRuleId,
  disabledRuleIds,
  onToggleDisable,
  whatIfResult,
  whatIfPending,
}: {
  rules: RuleEvaluation[];
  highlightRuleId: string | null;
  disabledRuleIds: string[];
  onToggleDisable: (ruleId: string) => void;
  whatIfResult: SimulateResult | null;
  whatIfPending: boolean;
}) {
  return (
    <div className="space-y-2">
      <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        Evaluation Waterfall
      </h3>

      {/* What-if comparison banner */}
      {disabledRuleIds.length > 0 && whatIfResult && (
        <div className="flex items-center gap-2 rounded-xl border border-accent/30 bg-accent/5 px-4 py-2 text-xs">
          <span className="font-semibold text-ink">What-if result:</span>
          <Badge variant={decisionBadge[whatIfResult.decision] ?? "default"}>
            {whatIfResult.decision}
          </Badge>
          {whatIfResult.reason && (
            <span className="text-muted-foreground">{whatIfResult.reason}</span>
          )}
        </div>
      )}
      {disabledRuleIds.length > 0 && whatIfPending && (
        <div className="rounded-xl border border-border bg-surface2/30 px-4 py-2 text-xs text-muted-foreground">
          Re-simulating without disabled rules...
        </div>
      )}

      {rules.map((rule, i) => {
        const isDisabled = disabledRuleIds.includes(rule.ruleId);
        const isHighlighted = highlightRuleId === rule.ruleId;
        return (
          <div
            key={rule.ruleId}
            id={`rule-${rule.ruleId}`}
            className={cn(
              "rounded-xl border-2 px-4 py-3 transition-all",
              isDisabled && "opacity-40",
              isHighlighted && "animate-pulse ring-2 ring-accent ring-offset-1",
              rule.matched && !isDisabled
                ? cn(
                    decisionColor[rule.decision] ?? "border-border",
                    "shadow-lg ring-2 ring-offset-1",
                    rule.decision === "allow" && "ring-success/30",
                    rule.decision === "deny" && "ring-danger/30",
                    rule.decision === "require_approval" && "ring-warning/30",
                    rule.decision === "throttle" && "ring-info/30",
                  )
                : !isHighlighted ? "border-border bg-surface2/30 opacity-60" : "border-border",
            )}
          >
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <span className="font-mono text-xs text-muted-foreground">
                  #{i + 1}
                </span>
                <span className={cn("text-sm font-medium", isDisabled ? "text-muted-foreground line-through" : "text-ink")}>
                  {rule.ruleId}
                </span>
              </div>
              <div className="flex items-center gap-2">
                {/* What-if toggle */}
                <button
                  type="button"
                  onClick={() => onToggleDisable(rule.ruleId)}
                  title={isDisabled ? "Re-enable rule" : "What-if: disable this rule"}
                  className={cn(
                    "rounded-full p-1 transition hover:bg-surface2",
                    isDisabled ? "text-accent" : "text-muted-foreground",
                  )}
                >
                  {isDisabled ? <Eye className="h-3.5 w-3.5" /> : <EyeOff className="h-3.5 w-3.5" />}
                </button>
                <Badge
                  variant={
                    rule.matched
                      ? (decisionBadge[rule.decision] ?? "default")
                      : "default"
                  }
                >
                  {rule.matched ? "MATCH" : "skip"}
                </Badge>
              </div>
            </div>
            {rule.matched && rule.reason && (
              <p className="mt-1 text-xs text-muted-foreground">{rule.reason}</p>
            )}
          </div>
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Result summary (with View Matching Rule button)
// ---------------------------------------------------------------------------

function ResultSummary({
  result,
  onViewMatchingRule,
}: {
  result: SimulateResult;
  onViewMatchingRule?: () => void;
}) {
  return (
    <Card className={cn("border-2", decisionColor[result.decision] ?? "border-border")}>
      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <h3 className="font-display text-lg font-semibold text-ink">
            Result
          </h3>
          <Badge variant={decisionBadge[result.decision] ?? "default"}>
            {result.decision}
          </Badge>
        </div>
        <div className="flex items-center gap-4 text-xs text-muted-foreground">
          <span>
            Matched rule: {result.matchedRule || "\u2014"}
          </span>
          <span>
            Eval: {result.evaluationTimeMs ?? "\u2014"}ms
          </span>
        </div>
        {result.matchedRule && onViewMatchingRule && (
          <button
            type="button"
            onClick={onViewMatchingRule}
            className="inline-flex items-center gap-1 text-xs font-semibold text-accent hover:underline"
          >
            <ArrowRight className="h-3 w-3" />
            View Matching Rule
          </button>
        )}
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// PolicySimulator
// ---------------------------------------------------------------------------

export type SimulatorMode = "simulate" | "explain";

interface PolicySimulatorProps {
  bundleId: string;
  mode?: SimulatorMode;
  initialCapabilities?: string[];
  initialRiskTags?: string[];
}

export function PolicySimulator({ bundleId, mode = "simulate", initialCapabilities, initialRiskTags }: PolicySimulatorProps) {
  const { tenantId, user, principalId: storedPrincipalId } = useAuth();
  const principalId =
    storedPrincipalId?.trim() || user?.id?.trim() || undefined;
  const [topic, setTopic] = useState("");
  const [capability, setCapability] = useState("");
  const [requires, setRequires] = useState<string[]>(() => initialCapabilities ?? []);
  const [riskTags, setRiskTags] = useState<string[]>(() => initialRiskTags ?? []);
  const [metadata, setMetadata] = useState<MetaRow[]>([]);
  const [formError, setFormError] = useState("");

  const simulate = useSimulatePolicy();
  const explain = useExplainPolicy();

  // What-if state
  const [disabledRuleIds, setDisabledRuleIds] = useState<string[]>([]);
  const whatIfSimulate = useSimulatePolicy();
  const whatIfDebounceRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Highlight state (for scroll-to-rule)
  const [highlightRuleId, setHighlightRuleId] = useState<string | null>(null);

  const handleTest = useCallback(() => {
    const trimmedTopic = topic.trim();
    if (!trimmedTopic) {
      setFormError("Topic is required.");
      return;
    }
    setFormError("");
    setDisabledRuleIds([]);
    const meta: Record<string, unknown> = {};
    for (const row of metadata) {
      if (row.key.trim()) {
        meta[row.key.trim()] = row.value;
      }
    }

    const requestBody = {
      topic: trimmedTopic,
      tenant: tenantId || undefined,
      principal_id: principalId,
      labels: meta,
      meta: {
        actor_id: principalId,
        capability: capability.trim() || undefined,
        risk_tags: riskTags,
        requires,
        labels: meta,
      },
    };

    if (mode === "explain") {
      explain.mutate({ request: requestBody });
    } else {
      simulate.mutate({ bundleId, request: requestBody });
    }
  }, [bundleId, topic, tenantId, principalId, capability, requires, riskTags, metadata, simulate, explain, mode]);

  const result = simulate.data;

  // Build evaluation waterfall from result details
  const waterfall: RuleEvaluation[] = (() => {
    if (!result?.details) return [];
    const evalPath = (result.details as Record<string, unknown>).evaluation_path;
    if (Array.isArray(evalPath)) {
      return (evalPath as Record<string, unknown>[]).map((entry) => ({
        ruleId: String(entry.rule_id ?? entry.ruleId ?? "unknown"),
        decision: String(entry.decision ?? ""),
        reason: String(entry.reason ?? ""),
        matched: Boolean(entry.matched),
      }));
    }
    // Fallback: mark matched rules
    if (result.matchedRule) {
      return [
        {
          ruleId: result.matchedRule,
          decision: result.decision,
          reason: result.reason ?? "",
          matched: true,
        },
      ];
    }
    return [];
  })();

  // What-if toggle handler — debounced re-simulation
  const handleToggleDisable = useCallback(
    (ruleId: string) => {
      setDisabledRuleIds((prev) => {
        const next = prev.includes(ruleId)
          ? prev.filter((id) => id !== ruleId)
          : [...prev, ruleId];
        return next;
      });
    },
    [],
  );

  // Re-simulate when disabledRuleIds changes (debounced 300ms)
  useEffect(() => {
    if (disabledRuleIds.length === 0) return;
    if (!result) return;

    if (whatIfDebounceRef.current) clearTimeout(whatIfDebounceRef.current);
    whatIfDebounceRef.current = setTimeout(() => {
      // Build content hint for the simulate endpoint — pass disabled rule IDs
      const meta: Record<string, unknown> = {};
      for (const row of metadata) {
        if (row.key.trim()) meta[row.key.trim()] = row.value;
      }
      whatIfSimulate.mutate({
        bundleId,
        request: {
          topic: topic.trim(),
          tenant: tenantId || undefined,
          principal_id: principalId,
          labels: meta,
          meta: {
            actor_id: principalId,
            capability: capability.trim() || undefined,
            risk_tags: riskTags,
            requires,
            labels: meta,
          },
        },
        content: JSON.stringify({ disabled_rules: disabledRuleIds }),
      });
    }, 300);

    return () => {
      if (whatIfDebounceRef.current) clearTimeout(whatIfDebounceRef.current);
    };
  }, [disabledRuleIds]);

  // Scroll to matched rule + flash highlight
  const handleViewMatchingRule = useCallback(() => {
    if (!result?.matchedRule) return;
    const el = document.getElementById(`rule-${result.matchedRule}`);
    if (el) {
      el.scrollIntoView({ behavior: "smooth", block: "center" });
      setHighlightRuleId(result.matchedRule);
      setTimeout(() => setHighlightRuleId(null), 2000);
    }
  }, [result]);

  return (
    <div className="space-y-6">
      {/* Form */}
      <Card>
        <div className="space-y-4">
          <h3 className="font-display text-lg font-semibold text-ink">
            {mode === "explain" ? "Explain Policy Decision" : "Simulate Policy Evaluation"}
          </h3>
          <p className="text-xs text-muted-foreground">
            {mode === "explain"
              ? "Shows the full decision reasoning chain \u2014 which rules were evaluated, in what order, and why each passed or failed."
              : "Test how a job with the given attributes would be evaluated."}
          </p>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted-foreground">
              Topic
            </label>
            <Input
              value={topic}
              onChange={(e) => setTopic(e.target.value)}
              placeholder="job.example.task"
            />
          </div>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted-foreground">
              Capability
            </label>
            <Input
              value={capability}
              onChange={(e) => setCapability(e.target.value)}
              placeholder="e.g. shell"
            />
          </div>

          <TagInput
            label="Requires"
            placeholder="e.g. file_write, network"
            values={requires}
            onChange={setRequires}
          />

          <TagInput
            label="Risk Tags"
            placeholder="e.g. destructive, pii, external"
            values={riskTags}
            onChange={setRiskTags}
          />

          <MetadataEditor rows={metadata} onChange={setMetadata} />

          <div className="flex items-center gap-3">
            <Button
              onClick={handleTest}
              disabled={mode === "explain" ? explain.isPending : simulate.isPending}
            >
              <Zap className="h-4 w-4" />
              {mode === "explain"
                ? explain.isPending ? "Explaining..." : "Explain"
                : simulate.isPending ? "Evaluating..." : "Test"}
            </Button>
            {formError && (
              <span className="text-xs text-danger">{formError}</span>
            )}
            {simulate.isError && mode === "simulate" && (
              <span className="text-xs text-danger">
                {simulate.error.message}
              </span>
            )}
            {explain.isError && mode === "explain" && (
              <span className="text-xs text-danger">
                {explain.error.message}
              </span>
            )}
          </div>
        </div>
      </Card>

      {/* Explain results */}
      {mode === "explain" && explain.data && (
        <ExplainResultPanel result={explain.data} />
      )}

      {/* Simulate results */}
      {mode === "simulate" && result && (
        <>
          <ResultSummary
            result={result}
            onViewMatchingRule={result.matchedRule ? handleViewMatchingRule : undefined}
          />

          {/* Deny suggestion */}
          <DenySuggestionCard
            result={result}
            capabilities={requires}
            riskTags={riskTags}
          />

          {waterfall.length > 0 && (
            <EvaluationWaterfall
              rules={waterfall}
              highlightRuleId={highlightRuleId}
              disabledRuleIds={disabledRuleIds}
              onToggleDisable={handleToggleDisable}
              whatIfResult={disabledRuleIds.length > 0 ? (whatIfSimulate.data ?? null) : null}
              whatIfPending={whatIfSimulate.isPending}
            />
          )}
        </>
      )}
    </div>
  );
}

