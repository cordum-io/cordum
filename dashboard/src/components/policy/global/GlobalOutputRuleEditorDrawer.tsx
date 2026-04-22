import { useEffect, useState } from "react";
import { X } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { GLOBAL_OUTPUT_DECISIONS, GLOBAL_OUTPUT_SEVERITIES } from "@/hooks/useOutputRules";
import type { GlobalPolicyOutputRule } from "@/types/policy";

interface GlobalOutputRuleEditorDrawerProps {
  open: boolean;
  rule?: GlobalPolicyOutputRule | null;
  nextRuleIndex?: number;
  onClose: () => void;
  onSave: (rule: GlobalPolicyOutputRule) => void;
}

function csvToList(raw: string): string[] {
  return raw.split(",").map((value) => value.trim()).filter(Boolean);
}

function listToCsv(values: string[]): string {
  return values.join(", ");
}

export function createEmptyGlobalOutputRule(nextIndex = 1): GlobalPolicyOutputRule {
  return {
    id: `output-rule-${nextIndex}`,
    enabled: true,
    severity: "medium",
    description: "",
    decision: "quarantine",
    reason: "",
    match: {
      tenants: [],
      topics: [],
      capabilities: [],
      riskTags: [],
      scanners: [],
      contentPatterns: [],
      keywords: [],
      contentTypes: [],
      detectors: [],
      outputSizeGt: undefined,
      maxOutputBytes: undefined,
      hasError: null,
    },
    source: {},
  };
}

function parseOptionalNumber(raw: string): number | undefined {
  if (!raw.trim()) return undefined;
  const parsed = Number(raw);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function numberToInput(value?: number): string {
  return typeof value === "number" && Number.isFinite(value) ? String(value) : "";
}

export function GlobalOutputRuleEditorDrawer({
  open,
  rule,
  nextRuleIndex = 1,
  onClose,
  onSave,
}: GlobalOutputRuleEditorDrawerProps) {
  const [draft, setDraft] = useState<GlobalPolicyOutputRule>(
    createEmptyGlobalOutputRule(nextRuleIndex),
  );
  const [error, setError] = useState("");

  useEffect(() => {
    if (!open) return;
    setDraft(rule ? structuredClone(rule) : createEmptyGlobalOutputRule(nextRuleIndex));
    setError("");
  }, [nextRuleIndex, open, rule]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-[135] flex justify-end">
      <button type="button" className="absolute inset-0 bg-black/50" aria-label="Close output rule editor" onClick={onClose} />
      <div className="relative h-full w-full max-w-lg overflow-y-auto border-l border-border bg-surface-1 p-5">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="font-display text-lg font-semibold text-foreground">
            {rule ? "Edit Output Rule" : "New Output Rule"}
          </h2>
          <button type="button" className="rounded-md p-2 text-muted-foreground hover:bg-surface-2" onClick={onClose} aria-label="Close output rule editor">
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="space-y-4 pb-20">
          <label className="block text-xs text-muted-foreground">
            Rule ID
            <input className="mt-1 h-8 w-full rounded-md border border-border bg-surface-2 px-3 text-xs text-foreground" value={draft.id} onChange={(event) => setDraft((previous) => ({ ...previous, id: event.target.value }))} />
          </label>
          <label className="inline-flex items-center gap-2 text-xs text-muted-foreground">
            <input type="checkbox" checked={draft.enabled} onChange={(event) => setDraft((previous) => ({ ...previous, enabled: event.target.checked }))} />
            enabled
          </label>
          <label className="block text-xs text-muted-foreground">
            severity
            <select className="mt-1 h-8 w-full rounded-md border border-border bg-surface-2 px-3 text-xs text-foreground" value={draft.severity} onChange={(event) => setDraft((previous) => ({ ...previous, severity: event.target.value as GlobalPolicyOutputRule["severity"] }))}>
              {GLOBAL_OUTPUT_SEVERITIES.map((value) => <option key={value} value={value}>{value}</option>)}
            </select>
          </label>
          <label className="block text-xs text-muted-foreground">
            decision
            <select className="mt-1 h-8 w-full rounded-md border border-border bg-surface-2 px-3 text-xs text-foreground" value={draft.decision} onChange={(event) => setDraft((previous) => ({ ...previous, decision: event.target.value as GlobalPolicyOutputRule["decision"] }))}>
              {GLOBAL_OUTPUT_DECISIONS.map((value) => <option key={value} value={value}>{value}</option>)}
            </select>
          </label>
          <label className="block text-xs text-muted-foreground">
            description
            <textarea rows={2} className="mt-1 w-full rounded-md border border-border bg-surface-2 px-3 py-2 text-xs text-foreground" value={draft.description} onChange={(event) => setDraft((previous) => ({ ...previous, description: event.target.value }))} />
          </label>
          <label className="block text-xs text-muted-foreground">
            reason
            <textarea rows={2} className="mt-1 w-full rounded-md border border-border bg-surface-2 px-3 py-2 text-xs text-foreground" value={draft.reason} onChange={(event) => setDraft((previous) => ({ ...previous, reason: event.target.value }))} />
          </label>
          {([
            { label: "detectors", value: draft.match.detectors, key: "detectors" },
            { label: "content_patterns", value: draft.match.contentPatterns, key: "contentPatterns" },
            { label: "topics", value: draft.match.topics, key: "topics" },
            { label: "scanners", value: draft.match.scanners, key: "scanners" },
          ] as const).map((field) => (
            <label key={field.key} className="block text-xs text-muted-foreground">
              {field.label} (comma separated)
              <input
                className="mt-1 h-8 w-full rounded-md border border-border bg-surface-2 px-3 text-xs text-foreground"
                value={listToCsv(field.value)}
                onChange={(event) =>
                  setDraft((previous) => ({
                    ...previous,
                    match: {
                      ...previous.match,
                      [field.key]: csvToList(event.target.value),
                    },
                  }))
                }
              />
            </label>
          ))}
          <div className="grid grid-cols-2 gap-3">
            <label className="block text-xs text-muted-foreground">
              output_size_gt
              <input
                className="mt-1 h-8 w-full rounded-md border border-border bg-surface-2 px-3 text-xs text-foreground"
                value={numberToInput(draft.match.outputSizeGt)}
                onChange={(event) =>
                  setDraft((previous) => ({
                    ...previous,
                    match: {
                      ...previous.match,
                      outputSizeGt: parseOptionalNumber(event.target.value),
                    },
                  }))
                }
              />
            </label>
            <label className="block text-xs text-muted-foreground">
              max_output_bytes
              <input
                className="mt-1 h-8 w-full rounded-md border border-border bg-surface-2 px-3 text-xs text-foreground"
                value={numberToInput(draft.match.maxOutputBytes)}
                onChange={(event) =>
                  setDraft((previous) => ({
                    ...previous,
                    match: {
                      ...previous.match,
                      maxOutputBytes: parseOptionalNumber(event.target.value),
                    },
                  }))
                }
              />
            </label>
          </div>
        </div>

        <div className="fixed bottom-0 right-0 z-[126] flex w-full max-w-lg items-center justify-between border-t border-border bg-surface-1 px-5 py-3">
          <span className="text-xs text-destructive">{error}</span>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={onClose}>Cancel</Button>
            <Button
              size="sm"
              onClick={() => {
                const id = draft.id.trim();
                if (!id) {
                  setError("Rule ID is required.");
                  return;
                }
                onSave({ ...draft, id });
              }}
            >
              Save rule
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
