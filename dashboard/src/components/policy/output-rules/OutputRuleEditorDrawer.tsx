import { X } from "lucide-react";
import type { GlobalPolicyOutputRule } from "@/types/policy";
import {
  createEmptyGlobalOutputRule,
  GlobalOutputRuleEditorDrawer,
} from "@/components/policy/global/GlobalOutputRuleEditorDrawer";

interface OutputRuleEditorDrawerProps {
  open: boolean;
  readOnly: boolean;
  rule?: GlobalPolicyOutputRule | null;
  nextRuleIndex: number;
  onClose: () => void;
  onSave: (rule: GlobalPolicyOutputRule) => void;
}

function ReadOnlyOutputRuleSummary({ rule }: { rule: GlobalPolicyOutputRule }) {
  return (
    <div className="space-y-4">
      <div className="rounded-2xl border border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10 px-3 py-2 text-xs text-[var(--color-warning)]">
        Viewer mode: output rule editor is read-only.
      </div>
      <p className="text-xs text-muted-foreground">
        <span className="font-mono text-foreground">rule_id:</span> {rule.id}
      </p>
      <p className="text-xs text-muted-foreground">
        <span className="font-mono text-foreground">decision:</span> {rule.decision}
      </p>
      <p className="text-xs text-muted-foreground">
        <span className="font-mono text-foreground">severity:</span> {rule.severity}
      </p>
      {rule.reason.trim() && (
        <p className="text-xs text-muted-foreground">
          <span className="font-mono text-foreground">reason:</span> {rule.reason.trim()}
        </p>
      )}
      <div>
        <p className="mb-2 text-[10px] font-mono uppercase tracking-wider text-muted-foreground">
          Match summary
        </p>
        <div className="flex flex-wrap gap-1.5">
          {rule.match.detectors.map((detector) => (
            <span key={`d-${detector}`} className="rounded bg-surface-2 px-2 py-0.5 text-[10px] font-mono text-muted-foreground">
              detector:{detector}
            </span>
          ))}
          {rule.match.contentPatterns.map((pattern) => (
            <span key={`p-${pattern}`} className="rounded bg-surface-2 px-2 py-0.5 text-[10px] font-mono text-muted-foreground">
              pattern:{pattern}
            </span>
          ))}
          {rule.match.detectors.length === 0 && rule.match.contentPatterns.length === 0 && (
            <span className="text-xs text-muted-foreground">No detectors/content patterns configured.</span>
          )}
        </div>
      </div>
    </div>
  );
}

export function OutputRuleEditorDrawer({
  open,
  readOnly,
  rule,
  nextRuleIndex,
  onClose,
  onSave,
}: OutputRuleEditorDrawerProps) {
  if (!readOnly) {
    return (
      <GlobalOutputRuleEditorDrawer
        open={open}
        rule={rule}
        nextRuleIndex={nextRuleIndex}
        onClose={onClose}
        onSave={onSave}
      />
    );
  }

  if (!open) return null;
  const resolvedRule = rule ?? createEmptyGlobalOutputRule(nextRuleIndex);

  return (
    <div className="fixed inset-0 z-[125] flex justify-end">
      <button className="absolute inset-0 bg-black/50" aria-label="Close output rule drawer" onClick={onClose} />
      <aside
        className="relative h-full w-full max-w-lg overflow-y-auto border-l border-border bg-surface-1 p-5"
        role="dialog"
        aria-modal="true"
        aria-labelledby="output-rule-readonly-title"
      >
        <div className="mb-4 flex items-center justify-between">
          <h2 id="output-rule-readonly-title" className="font-display text-lg font-semibold text-foreground">
            View Output Rule
          </h2>
          <button className="rounded-md p-2 text-muted-foreground hover:bg-surface-2" onClick={onClose} aria-label="Close output rule drawer">
            <X className="h-4 w-4" />
          </button>
        </div>

        <ReadOnlyOutputRuleSummary rule={resolvedRule} />
      </aside>
    </div>
  );
}
