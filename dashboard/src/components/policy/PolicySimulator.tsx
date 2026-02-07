import { useState, useCallback } from "react";
import { Plus, Trash2, Zap } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Input } from "../ui/Input";
import { useSimulatePolicy, type SimulateResult } from "../../hooks/usePolicies";
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

const decisionBadge: Record<string, "success" | "danger" | "warning" | "info" | "default"> = {
  allow: "success",
  deny: "danger",
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
      <label className="mb-1 block text-xs font-semibold text-muted">
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
        <label className="text-xs font-semibold text-muted">Metadata</label>
        <Button variant="ghost" size="sm" type="button" onClick={addRow}>
          <Plus className="h-3.5 w-3.5" />
          Add
        </Button>
      </div>
      {rows.length === 0 && (
        <p className="text-xs text-muted">No metadata entries.</p>
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
// Evaluation waterfall
// ---------------------------------------------------------------------------

function EvaluationWaterfall({
  rules,
}: {
  rules: RuleEvaluation[];
}) {
  return (
    <div className="space-y-2">
      <h3 className="text-xs font-semibold uppercase tracking-wide text-muted">
        Evaluation Waterfall
      </h3>
      {rules.map((rule, i) => (
        <div
          key={rule.ruleId}
          className={cn(
            "rounded-xl border-2 px-4 py-3 transition-all",
            rule.matched
              ? cn(
                  decisionColor[rule.decision] ?? "border-border",
                  "shadow-lg ring-2 ring-offset-1",
                  rule.decision === "allow" && "ring-success/30",
                  rule.decision === "deny" && "ring-danger/30",
                  rule.decision === "require_approval" && "ring-warning/30",
                  rule.decision === "throttle" && "ring-info/30",
                )
              : "border-border bg-surface2/30 opacity-60",
          )}
        >
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <span className="font-mono text-[10px] text-muted">
                #{i + 1}
              </span>
              <span className="text-sm font-medium text-ink">
                {rule.ruleId}
              </span>
            </div>
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
          {rule.matched && rule.reason && (
            <p className="mt-1 text-xs text-muted">{rule.reason}</p>
          )}
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Result summary
// ---------------------------------------------------------------------------

function ResultSummary({ result }: { result: SimulateResult }) {
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
        <div className="flex items-center gap-4 text-xs text-muted">
          <span>
            Matched rule: {result.matchedRule || "—"}
          </span>
          <span>
            Eval: {result.evaluationTimeMs ?? "—"}ms
          </span>
        </div>
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// PolicySimulator
// ---------------------------------------------------------------------------

export function PolicySimulator({ bundleId }: { bundleId: string }) {
  const { tenantId, user, principalId: storedPrincipalId } = useAuth();
  const principalId =
    storedPrincipalId?.trim() || user?.id?.trim() || undefined;
  const [topic, setTopic] = useState("");
  const [capability, setCapability] = useState("");
  const [requires, setRequires] = useState<string[]>([]);
  const [riskTags, setRiskTags] = useState<string[]>([]);
  const [metadata, setMetadata] = useState<MetaRow[]>([]);
  const [formError, setFormError] = useState("");

  const simulate = useSimulatePolicy();

  const handleTest = useCallback(() => {
    const trimmedTopic = topic.trim();
    if (!trimmedTopic) {
      setFormError("Topic is required.");
      return;
    }
    setFormError("");
    const meta: Record<string, unknown> = {};
    for (const row of metadata) {
      if (row.key.trim()) {
        meta[row.key.trim()] = row.value;
      }
    }

    simulate.mutate({
      bundleId,
      request: {
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
      },
    });
  }, [bundleId, topic, tenantId, principalId, capability, requires, riskTags, metadata, simulate]);

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

  return (
    <div className="space-y-6">
      {/* Form */}
      <Card>
        <div className="space-y-4">
          <h3 className="font-display text-lg font-semibold text-ink">
            Simulate Policy Evaluation
          </h3>
          <p className="text-xs text-muted">
            Test how a job with the given attributes would be evaluated.
          </p>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted">
              Topic
            </label>
            <Input
              value={topic}
              onChange={(e) => setTopic(e.target.value)}
              placeholder="job.example.task"
            />
          </div>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted">
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
              disabled={simulate.isPending}
            >
              <Zap className="h-4 w-4" />
              {simulate.isPending ? "Evaluating..." : "Test"}
            </Button>
            {formError && (
              <span className="text-xs text-danger">{formError}</span>
            )}
            {simulate.isError && (
              <span className="text-xs text-danger">
                {simulate.error.message}
              </span>
            )}
          </div>
        </div>
      </Card>

      {/* Results */}
      {result && (
        <>
          <ResultSummary result={result} />
          {waterfall.length > 0 && <EvaluationWaterfall rules={waterfall} />}
        </>
      )}
    </div>
  );
}
