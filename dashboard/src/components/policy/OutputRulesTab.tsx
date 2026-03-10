import { useEffect, useMemo, useState } from "react";
import { Pencil, Plus, RefreshCw } from "lucide-react";
import {
  useOutputRules,
  useToggleOutputRule,
  useUpsertOutputRule,
} from "../../hooks/useOutputRules";
import type { OutputRuleDraftInput } from "../../hooks/useOutputRules";
import type { OutputRule } from "../../types/policy";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Input } from "../ui/Input";
import { Textarea } from "../ui/Textarea";
import { OutputRuleDetail } from "./OutputRuleDetail";

function severityVariant(severity?: string): "danger" | "warning" | "info" | "default" {
  switch ((severity || "").toLowerCase()) {
    case "critical":
      return "danger";
    case "high":
      return "warning";
    case "medium":
      return "info";
    default:
      return "default";
  }
}

function decisionVariant(decision?: string): "danger" | "warning" | "success" | "default" {
  switch ((decision || "").toLowerCase()) {
    case "deny":
    case "quarantine":
      return "danger";
    case "redact":
      return "warning";
    case "allow":
      return "success";
    default:
      return "default";
  }
}

function toList(raw: string): string[] {
  return raw
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function fromList(values?: string[]): string {
  return (values ?? []).join(", ");
}

function extractBundleId(rule: OutputRule | null): string {
  if (!rule?.source || typeof rule.source !== "object") return "";
  const candidate = (rule.source as Record<string, unknown>).fragment_id;
  return typeof candidate === "string" ? candidate.trim() : "";
}

function OutputRuleEditorModal({
  open,
  initialRule,
  defaultBundleId,
  isPending,
  onClose,
  onSave,
}: {
  open: boolean;
  initialRule: OutputRule | null;
  defaultBundleId: string;
  isPending: boolean;
  onClose: () => void;
  onSave: (args: {
    bundleId: string;
    existingRuleId?: string;
    draft: OutputRuleDraftInput;
  }) => void;
}) {
  const [bundleId, setBundleId] = useState(defaultBundleId);
  const [id, setID] = useState("");
  const [description, setDescription] = useState("");
  const [pattern, setPattern] = useState("");
  const [decision, setDecision] = useState("deny");
  const [severity, setSeverity] = useState("high");
  const [enabled, setEnabled] = useState(true);
  const [reason, setReason] = useState("");
  const [topics, setTopics] = useState("");
  const [scanners, setScanners] = useState("");
  const [sampleText, setSampleText] = useState("");
  const [formError, setFormError] = useState("");

  useEffect(() => {
    if (!open) return;
    setBundleId(extractBundleId(initialRule) || defaultBundleId || "");
    setID(initialRule?.id ?? "");
    setDescription(initialRule?.description ?? "");
    setPattern(initialRule?.patterns?.[0] ?? initialRule?.patternPreview ?? "");
    setDecision((initialRule?.decision || "deny").toLowerCase());
    setSeverity((initialRule?.severity || "high").toLowerCase());
    setEnabled(initialRule?.enabled ?? true);
    setReason(initialRule?.reason ?? "");
    setTopics(fromList(initialRule?.topics));
    setScanners(fromList(initialRule?.scanners));
    setSampleText("");
    setFormError("");
  }, [defaultBundleId, initialRule, open]);

  const tester = useMemo(() => {
    const source = pattern.trim();
    if (!source) {
      return { valid: false, matched: false, message: "Pattern is required." };
    }
    try {
      const re = new RegExp(source);
      if (!sampleText) {
        return {
          valid: true,
          matched: false,
          message: "Enter sample output text to test this pattern.",
        };
      }
      const matched = re.test(sampleText);
      return {
        valid: true,
        matched,
        message: matched ? "Pattern matched sample content." : "No match for sample content.",
      };
    } catch (error) {
      return {
        valid: false,
        matched: false,
        message: error instanceof Error ? error.message : "Invalid regex pattern",
      };
    }
  }, [pattern, sampleText]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <Card className="max-h-[90vh] w-full max-w-3xl overflow-auto">
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <h3 className="font-display text-lg font-semibold text-ink">
              {initialRule ? "Edit Output Rule" : "Add Output Rule"}
            </h3>
            <Button variant="ghost" size="sm" type="button" onClick={onClose}>
              Close
            </Button>
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="mb-1 block text-xs font-semibold text-muted-foreground">Bundle ID</label>
              <Input
                value={bundleId}
                onChange={(event) => setBundleId(event.target.value)}
                placeholder="secops/default"
              />
            </div>
            <div>
              <label className="mb-1 block text-xs font-semibold text-muted-foreground">Rule ID</label>
              <Input
                value={id}
                onChange={(event) => setID(event.target.value)}
                placeholder="out.secret.quarantine"
              />
            </div>
            <div>
              <label className="mb-1 block text-xs font-semibold text-muted-foreground">Decision</label>
              <select
                className="w-full rounded-xl border border-border bg-surface1 px-3 py-2 text-sm text-ink"
                value={decision}
                onChange={(event) => setDecision(event.target.value)}
              >
                <option value="deny">Deny</option>
                <option value="redact">Redact</option>
              </select>
            </div>
            <div>
              <label className="mb-1 block text-xs font-semibold text-muted-foreground">Severity</label>
              <select
                className="w-full rounded-xl border border-border bg-surface1 px-3 py-2 text-sm text-ink"
                value={severity}
                onChange={(event) => setSeverity(event.target.value)}
              >
                <option value="critical">Critical</option>
                <option value="high">High</option>
                <option value="medium">Medium</option>
                <option value="low">Low</option>
              </select>
            </div>
          </div>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted-foreground">Description</label>
            <Input
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              placeholder="Block output that contains cloud credentials"
            />
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <label className="mb-1 block text-xs font-semibold text-muted-foreground">Topics (comma separated)</label>
              <Input
                value={topics}
                onChange={(event) => setTopics(event.target.value)}
                placeholder="job.*, job.secure.*"
              />
            </div>
            <div>
              <label className="mb-1 block text-xs font-semibold text-muted-foreground">Scanners (comma separated)</label>
              <Input
                value={scanners}
                onChange={(event) => setScanners(event.target.value)}
                placeholder="regex, secret"
              />
            </div>
          </div>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted-foreground">Pattern (regex)</label>
            <Input
              value={pattern}
              onChange={(event) => setPattern(event.target.value)}
              placeholder="AKIA[0-9A-Z]{16}"
            />
          </div>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted-foreground">Reason</label>
            <Input
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              placeholder="Potential credential disclosure"
            />
          </div>

          <div className="rounded-xl border border-border bg-surface2/30 p-3">
            <div className="mb-2 flex items-center justify-between">
              <label className="text-xs font-semibold text-muted-foreground">Pattern Tester</label>
              <Badge variant={tester.valid ? (tester.matched ? "success" : "warning") : "danger"}>
                {tester.valid ? (tester.matched ? "MATCH" : "NO MATCH") : "INVALID"}
              </Badge>
            </div>
            <Textarea
              value={sampleText}
              onChange={(event) => setSampleText(event.target.value)}
              rows={4}
              placeholder="Paste sample output content to test the regex."
            />
            <p className="mt-2 text-xs text-muted-foreground">{tester.message}</p>
          </div>

          <label className="inline-flex items-center gap-2 text-sm text-ink">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(event) => setEnabled(event.target.checked)}
            />
            Enabled
          </label>

          {formError && (
            <div className="rounded-xl border border-danger/30 bg-danger/5 px-3 py-2 text-xs text-danger">
              {formError}
            </div>
          )}

          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" type="button" onClick={onClose}>
              Cancel
            </Button>
            <Button
              size="sm"
              type="button"
              disabled={isPending}
              onClick={() => {
                const normalizedBundleID = bundleId.trim();
                const normalizedID = id.trim();
                const normalizedPattern = pattern.trim();
                if (!normalizedBundleID) {
                  setFormError("Bundle ID is required.");
                  return;
                }
                if (!normalizedID) {
                  setFormError("Rule ID is required.");
                  return;
                }
                if (!normalizedPattern) {
                  setFormError("Pattern is required.");
                  return;
                }
                if (!tester.valid) {
                  setFormError("Pattern must be a valid regular expression.");
                  return;
                }
                setFormError("");
                onSave({
                  bundleId: normalizedBundleID,
                  existingRuleId: initialRule?.id,
                  draft: {
                    id: normalizedID,
                    description: description.trim(),
                    pattern: normalizedPattern,
                    decision,
                    severity,
                    enabled,
                    reason: reason.trim(),
                    topics: toList(topics),
                    scanners: toList(scanners),
                  },
                });
              }}
            >
              {isPending ? "Saving..." : initialRule ? "Save Changes" : "Add Rule"}
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}

export function OutputRulesTab({ activeBundleId = "" }: { activeBundleId?: string }) {
  const { data = [], isLoading, isError, refetch, isFetching } = useOutputRules();
  const toggleOutputRule = useToggleOutputRule();
  const upsertOutputRule = useUpsertOutputRule();
  const rules = data;

  const [selectedRuleID, setSelectedRuleID] = useState<string | null>(null);
  const [editingRule, setEditingRule] = useState<OutputRule | null>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const pendingRuleID = toggleOutputRule.variables?.id;
  const pendingEnabled = toggleOutputRule.variables?.enabled;
  const selectedRule = useMemo(
    () => rules.find((rule) => rule.id === selectedRuleID) ?? null,
    [rules, selectedRuleID],
  );

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="font-display text-lg font-semibold text-ink">Output Rules</h3>
          <p className="text-xs text-muted-foreground">
            Rules applied to job output content after worker execution.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            type="button"
            onClick={() => refetch()}
            disabled={isFetching}
          >
            <RefreshCw className={`h-3.5 w-3.5 ${isFetching ? "animate-spin" : ""}`} />
            Refresh
          </Button>
          <Button
            size="sm"
            type="button"
            onClick={() => {
              setEditingRule(null);
              setEditorOpen(true);
            }}
          >
            <Plus className="h-3.5 w-3.5" />
            Add Rule
          </Button>
        </div>
      </div>

      <div className="surface-card overflow-hidden rounded-2xl">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="border-b border-border">
              <tr>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">Rule ID</th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">Topics</th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">Scanners</th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">Pattern</th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">Decision</th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">Severity</th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">Enabled</th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {isLoading &&
                Array.from({ length: 6 }, (_, i) => (
                  <tr key={i} className="animate-pulse">
                    {Array.from({ length: 8 }, (_, j) => (
                      <td key={j} className="px-4 py-3">
                        <div className="h-4 w-3/4 rounded bg-surface2" />
                      </td>
                    ))}
                  </tr>
                ))}

              {!isLoading && isError && (
                <tr>
                  <td colSpan={8} className="px-4 py-12 text-center text-muted-foreground">
                    Failed to load output policy rules.
                  </td>
                </tr>
              )}

              {!isLoading && !isError && rules.length === 0 && (
                <tr>
                  <td colSpan={8} className="px-4 py-12 text-center text-muted-foreground">
                    No output rules configured.
                  </td>
                </tr>
              )}

              {!isLoading &&
                rules.map((rule) => {
                  const enabled =
                    toggleOutputRule.isPending && pendingRuleID === rule.id
                      ? pendingEnabled ?? rule.enabled
                      : rule.enabled;
                  return (
                    <tr
                      key={rule.id}
                      className="cursor-pointer transition-colors hover:bg-surface2/60"
                      onClick={() => setSelectedRuleID(rule.id)}
                    >
                      <td className="px-4 py-3 font-mono text-xs text-ink">{rule.id}</td>
                      <td className="px-4 py-3 text-xs text-muted-foreground">
                        {(rule.topics ?? []).join(", ") || "\u2014"}
                      </td>
                      <td className="px-4 py-3 text-xs text-muted-foreground">
                        {(rule.scanners ?? []).join(", ") || "\u2014"}
                      </td>
                      <td className="px-4 py-3 font-mono text-xs text-muted-foreground">
                        {rule.patternPreview || rule.patterns?.[0] || "\u2014"}
                      </td>
                      <td className="px-4 py-3">
                        <Badge variant={decisionVariant(rule.decision)}>
                          {(rule.decision || "allow").toUpperCase()}
                        </Badge>
                      </td>
                      <td className="px-4 py-3">
                        <Badge variant={severityVariant(rule.severity)}>
                          {(rule.severity || "low").toUpperCase()}
                        </Badge>
                      </td>
                      <td className="px-4 py-3">
                        <button
                          type="button"
                          role="switch"
                          aria-checked={enabled}
                          aria-label={enabled ? "Disable output rule" : "Enable output rule"}
                          disabled={toggleOutputRule.isPending && pendingRuleID === rule.id}
                          className={`relative inline-flex h-5 w-9 rounded-full border-2 border-transparent transition-colors ${
                            enabled ? "bg-accent" : "bg-surface2"
                          }`}
                          onClick={(e) => {
                            e.stopPropagation();
                            if (!toggleOutputRule.isPending) {
                              toggleOutputRule.mutate({
                                id: rule.id,
                                enabled: !enabled,
                              });
                            }
                          }}
                        >
                          <span
                            className={`pointer-events-none inline-block h-4 w-4 rounded-full bg-card shadow transition-transform ${
                              enabled ? "translate-x-4" : "translate-x-0"
                            }`}
                          />
                        </button>
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2">
                          <button
                            type="button"
                            className="text-xs font-semibold text-accent hover:underline"
                            onClick={(e) => {
                              e.stopPropagation();
                              setSelectedRuleID(rule.id);
                            }}
                          >
                            View
                          </button>
                          <button
                            type="button"
                            className="inline-flex items-center gap-1 text-xs font-semibold text-accent hover:underline"
                            onClick={(e) => {
                              e.stopPropagation();
                              setEditingRule(rule);
                              setEditorOpen(true);
                            }}
                          >
                            <Pencil className="h-3 w-3" />
                            Edit
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
            </tbody>
          </table>
        </div>
      </div>

      <OutputRuleEditorModal
        open={editorOpen}
        initialRule={editingRule}
        defaultBundleId={activeBundleId}
        isPending={upsertOutputRule.isPending}
        onClose={() => {
          if (upsertOutputRule.isPending) return;
          setEditorOpen(false);
          setEditingRule(null);
        }}
        onSave={(args) => {
          upsertOutputRule.mutate(args, {
            onSuccess: () => {
              setEditorOpen(false);
              setEditingRule(null);
            },
          });
        }}
      />

      <OutputRuleDetail
        rule={selectedRule}
        onClose={() => setSelectedRuleID(null)}
      />
    </div>
  );
}
