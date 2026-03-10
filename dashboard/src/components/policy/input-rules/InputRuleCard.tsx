import { ArrowDown, ArrowUp, Eye, Pencil, Trash2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { getAdvancedConfiguredSummary } from "@/lib/policy-studio/globalRuleEditorState";
import type { GlobalPolicyInputRule } from "@/types/policy";

interface InputRuleCardProps {
  index: number;
  total: number;
  rule: GlobalPolicyInputRule;
  canEdit: boolean;
  onView: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onMoveUp: () => void;
  onMoveDown: () => void;
  onFocusRule?: () => void;
}

function decisionClasses(decision: GlobalPolicyInputRule["decision"]): string {
  switch (decision) {
    case "allow":
      return "bg-[var(--color-success)]/15 text-[var(--color-success)] border-[var(--color-success)]/30";
    case "deny":
      return "bg-destructive/15 text-destructive border-destructive/30";
    case "require_approval":
      return "bg-[var(--color-warning)]/15 text-[var(--color-warning)] border-[var(--color-warning)]/30";
    case "allow_with_constraints":
      return "bg-[var(--color-info)]/15 text-[var(--color-info)] border-[var(--color-info)]/30";
    default:
      return "bg-primary/15 text-primary border-primary/30";
  }
}

function buildMatchChips(rule: GlobalPolicyInputRule): string[] {
  const chips: string[] = [];
  chips.push(...rule.match.topics.map((topic) => `topic:${topic}`));
  chips.push(...rule.match.capabilities.map((capability) => `cap:${capability}`));
  chips.push(...rule.match.riskTags.map((riskTag) => `risk:${riskTag}`));
  chips.push(...rule.match.actorIds.map((actorId) => `actor:${actorId}`));
  chips.push(...Object.entries(rule.match.labels).map(([key, value]) => `${key}=${value}`));
  if (rule.match.secretsPresent !== null) {
    chips.push(`secrets_present:${rule.match.secretsPresent ? "true" : "false"}`);
  }
  return chips;
}

export function InputRuleCard({
  index,
  total,
  rule,
  canEdit,
  onView,
  onEdit,
  onDelete,
  onMoveUp,
  onMoveDown,
  onFocusRule,
}: InputRuleCardProps) {
  const matchChips = buildMatchChips(rule);
  const advanced = getAdvancedConfiguredSummary(rule);

  return (
    <article
      tabIndex={0}
      onFocus={onFocusRule}
      onKeyDown={(event) => {
        if (!canEdit) return;
        if (event.altKey && event.key === "ArrowUp") {
          event.preventDefault();
          onMoveUp();
        }
        if (event.altKey && event.key === "ArrowDown") {
          event.preventDefault();
          onMoveDown();
        }
      }}
      className="instrument-card p-4 outline-none focus:ring-2 focus:ring-cordum/40"
    >
      <header className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <span className="rounded bg-surface-2 px-2 py-1 text-[10px] font-mono text-muted-foreground">
            #{index + 1}
          </span>
          <h3 className="text-sm font-semibold text-foreground">{rule.id}</h3>
          <span
            className={cn(
              "rounded border px-2 py-0.5 text-[10px] font-mono uppercase",
              decisionClasses(rule.decision),
            )}
          >
            {rule.decision}
          </span>
          {advanced.count > 0 && (
            <span className="rounded border border-cordum/40 bg-cordum/10 px-2 py-0.5 text-[10px] font-mono text-cordum-foreground">
              adv:{advanced.count}
            </span>
          )}
        </div>

        <div className="flex items-center gap-1">
          {canEdit && (
            <>
              <button
                aria-label={`Move ${rule.id} up`}
                className="rounded p-1 text-muted-foreground hover:bg-surface-2 disabled:opacity-40"
                disabled={index === 0}
                onClick={onMoveUp}
              >
                <ArrowUp className="h-3.5 w-3.5" />
              </button>
              <button
                aria-label={`Move ${rule.id} down`}
                className="rounded p-1 text-muted-foreground hover:bg-surface-2 disabled:opacity-40"
                disabled={index === total - 1}
                onClick={onMoveDown}
              >
                <ArrowDown className="h-3.5 w-3.5" />
              </button>
              <button
                aria-label={`Edit ${rule.id}`}
                className="rounded p-1 text-muted-foreground hover:bg-surface-2"
                onClick={onEdit}
              >
                <Pencil className="h-3.5 w-3.5" />
              </button>
            </>
          )}
          {!canEdit && (
            <button
              aria-label={`View ${rule.id}`}
              className="rounded p-1 text-muted-foreground hover:bg-surface-2"
              onClick={onView}
            >
              <Eye className="h-3.5 w-3.5" />
            </button>
          )}
          {canEdit && (
            <button
              aria-label={`Delete ${rule.id}`}
              className="rounded p-1 text-destructive hover:bg-destructive/10"
              onClick={onDelete}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      </header>

      {rule.reason && <p className="mb-3 text-xs text-muted-foreground">{rule.reason}</p>}

      <div className="flex flex-wrap gap-1.5">
        {matchChips.length === 0 && (
          <span className="text-xs text-muted-foreground">No match filters configured.</span>
        )}
        {matchChips.slice(0, 10).map((chip) => (
          <span
            key={`${rule.id}-${chip}`}
            className="rounded bg-surface-2 px-2 py-0.5 text-[10px] font-mono text-muted-foreground"
          >
            {chip}
          </span>
        ))}
        {matchChips.length > 10 && (
          <span className="rounded bg-surface-2 px-2 py-0.5 text-[10px] font-mono text-muted-foreground">
            +{matchChips.length - 10} more
          </span>
        )}
      </div>
    </article>
  );
}
