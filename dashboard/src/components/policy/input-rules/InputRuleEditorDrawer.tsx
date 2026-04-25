import { X } from "lucide-react";
import {
  createEmptyGlobalInputRule,
  GlobalRuleEditorDrawer,
} from "@/components/policy/global/GlobalRuleEditorDrawer";
import { useDialogA11y } from "@/hooks/useDialogA11y";
import type { GlobalPolicyInputRule } from "@/types/policy";

interface InputRuleEditorDrawerProps {
  open: boolean;
  readOnly: boolean;
  rule?: GlobalPolicyInputRule | null;
  nextRuleIndex: number;
  existingRuleIds: string[];
  onClose: () => void;
  onSave: (rule: GlobalPolicyInputRule) => void;
}

function ReadOnlyRuleSummary({ rule }: { rule: GlobalPolicyInputRule }) {
  const matchSummary = [
    ...rule.match.topics.map((topic) => `topic:${topic}`),
    ...rule.match.capabilities.map((capability) => `cap:${capability}`),
    ...rule.match.riskTags.map((riskTag) => `risk:${riskTag}`),
    ...rule.match.actorIds.map((actorId) => `actor:${actorId}`),
  ];

  return (
    <div className="space-y-4">
      <div className="rounded-2xl border border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10 px-3 py-2 text-xs text-[var(--color-warning)]">
        Viewer mode: this drawer is read-only. Write operations are restricted by role.
      </div>
      <div className="space-y-2">
        <p className="text-xs text-muted-foreground">
          <span className="font-mono text-foreground">rule_id:</span> {rule.id}
        </p>
        <p className="text-xs text-muted-foreground">
          <span className="font-mono text-foreground">decision:</span> {rule.decision}
        </p>
        {rule.reason.trim() && (
          <p className="text-xs text-muted-foreground">
            <span className="font-mono text-foreground">reason:</span> {rule.reason.trim()}
          </p>
        )}
      </div>
      <div>
        <p className="mb-2 text-xs font-mono uppercase tracking-wider text-muted-foreground">
          Match summary
        </p>
        <div className="flex flex-wrap gap-1.5">
          {matchSummary.length === 0 && (
            <span className="text-xs text-muted-foreground">No explicit match filters configured.</span>
          )}
          {matchSummary.map((entry) => (
            <span
              key={`${rule.id}-${entry}`}
              className="rounded bg-surface-2 px-2 py-0.5 text-xs font-mono text-muted-foreground"
            >
              {entry}
            </span>
          ))}
        </div>
      </div>
    </div>
  );
}

export function InputRuleEditorDrawer({
  open,
  readOnly,
  rule,
  nextRuleIndex,
  existingRuleIds,
  onClose,
  onSave,
}: InputRuleEditorDrawerProps) {
  const dialogRef = useDialogA11y(onClose, { enabled: readOnly && open });
  if (!readOnly) {
    return (
      <GlobalRuleEditorDrawer
        open={open}
        rule={rule}
        nextRuleIndex={nextRuleIndex}
        existingRuleIds={existingRuleIds}
        onClose={onClose}
        onSave={onSave}
      />
    );
  }

  if (!open) return null;
  const resolvedRule = rule ?? createEmptyGlobalInputRule(nextRuleIndex);

  return (
    <div className="fixed inset-0 z-[120] flex justify-end">
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
        aria-labelledby="input-rule-readonly-title"
      >
        <div className="mb-4 flex items-center justify-between">
          <h2 className="font-display text-lg font-semibold text-foreground" id="input-rule-readonly-title">
            View Rule
          </h2>
          <button
            type="button"
            className="rounded-xl p-2 text-muted-foreground hover:bg-surface-2"
            aria-label="Close editor"
            onClick={onClose}
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <ReadOnlyRuleSummary rule={resolvedRule} />

        <div className="sticky bottom-0 mt-6 border-t border-border bg-surface-1 pt-3">
          <div className="flex justify-end">
            <button
              type="button"
              className="inline-flex items-center justify-center rounded-xl border border-border px-3 py-2 text-xs text-foreground hover:bg-surface-2"
              onClick={onClose}
            >
              Close
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

