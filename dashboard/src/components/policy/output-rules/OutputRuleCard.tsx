import { ArrowDown, ArrowUp, Eye, Pencil, Power, Trash2 } from "lucide-react";
import { cn } from "@/lib/utils";
import type { GlobalPolicyOutputRule } from "@/types/policy";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";

interface OutputRuleCardProps {
  index: number;
  total: number;
  rule: GlobalPolicyOutputRule;
  canEdit: boolean;
  onView: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onMoveUp: () => void;
  onMoveDown: () => void;
  onToggleEnabled: () => void;
  onFocusRule?: () => void;
}

function severityClass(severity: GlobalPolicyOutputRule["severity"]): string {
  switch (severity) {
    case "critical":
      return "text-destructive bg-destructive/10 border-destructive/20";
    case "high":
      return "text-[var(--color-warning)] bg-[var(--color-warning)]/10 border-[var(--color-warning)]/20";
    case "medium":
      return "text-[var(--color-warning)] bg-[var(--color-warning)]/10 border-[var(--color-warning)]/20";
    default:
      return "text-[var(--color-info)] bg-[var(--color-info)]/10 border-[var(--color-info)]/20";
  }
}

export function OutputRuleCard({
  index,
  total,
  rule,
  canEdit,
  onView,
  onEdit,
  onDelete,
  onMoveUp,
  onMoveDown,
  onToggleEnabled,
  onFocusRule,
}: OutputRuleCardProps) {
  return (
    <article
      tabIndex={0}
      onFocus={onFocusRule}
      className="instrument-card outline-none focus:ring-2 focus:ring-cordum/40"
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
    >
      <header className="mb-4 flex flex-wrap items-start justify-between gap-4">
        <div className="flex flex-wrap items-center gap-2">
          <span className="rounded bg-surface-2 px-2 py-0.5 text-[10px] font-mono text-muted-foreground border border-border">
            #{index + 1}
          </span>
          <h3 className="text-sm font-semibold font-display text-foreground">{rule.id}</h3>
          <SafetyDecisionBadge decision={rule.decision} />
          <span className={cn("inline-flex items-center px-2 py-0.5 rounded text-[10px] font-mono font-bold uppercase border", severityClass(rule.severity))}>
            {rule.severity}
          </span>
          {!rule.enabled && (
            <span className="rounded bg-surface-2 px-2 py-0.5 text-[10px] font-mono text-muted-foreground border border-border">
              DISABLED
            </span>
          )}
        </div>
        <div className="flex items-center gap-1 shrink-0">
          {canEdit && (
            <>
              <button
                aria-label={`Toggle ${rule.id}`}
                className="rounded p-1.5 text-muted-foreground hover:bg-surface-2 transition-colors"
                onClick={onToggleEnabled}
                title={rule.enabled ? "Disable Rule" : "Enable Rule"}
              >
                <Power className="h-3.5 w-3.5" />
              </button>
              <button
                aria-label={`Move ${rule.id} up`}
                className="rounded p-1.5 text-muted-foreground hover:bg-surface-2 disabled:opacity-40 transition-colors"
                disabled={index === 0}
                onClick={onMoveUp}
                title="Move Up (Alt + Up)"
              >
                <ArrowUp className="h-3.5 w-3.5" />
              </button>
              <button
                aria-label={`Move ${rule.id} down`}
                className="rounded p-1.5 text-muted-foreground hover:bg-surface-2 disabled:opacity-40 transition-colors"
                disabled={index === total - 1}
                onClick={onMoveDown}
                title="Move Down (Alt + Down)"
              >
                <ArrowDown className="h-3.5 w-3.5" />
              </button>
              <button 
                aria-label={`Edit ${rule.id}`} 
                className="rounded p-1.5 text-muted-foreground hover:bg-surface-2 transition-colors" 
                onClick={onEdit}
                title="Edit Rule"
              >
                <Pencil className="h-3.5 w-3.5" />
              </button>
              <button 
                aria-label={`Delete ${rule.id}`} 
                className="rounded p-1.5 text-destructive hover:bg-destructive/10 transition-colors"
                onClick={onDelete}
                title="Delete Rule"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            </>
          )}
          {!canEdit && (
            <button 
              aria-label={`View ${rule.id}`} 
              className="rounded p-1.5 text-muted-foreground hover:bg-surface-2 transition-colors" 
              onClick={onView}
              title="View Rule"
            >
              <Eye className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      </header>

      {rule.description && <p className="mb-3 text-xs text-muted-foreground leading-relaxed">{rule.description}</p>}
      {rule.reason && <p className="mb-3 text-[11px] font-mono text-muted-foreground/80 italic">reason: {rule.reason}</p>}

      <div className="surface-inset p-3 flex flex-wrap gap-2">
        {rule.match.detectors.map((value) => (
          <span key={`detector-${value}`} className="rounded bg-[var(--color-warning)]/15 border border-[var(--color-warning)]/20 px-2 py-0.5 text-[10px] font-mono text-[var(--color-warning)]">
            detector:{value}
          </span>
        ))}
        {rule.match.contentPatterns.map((value) => (
          <span key={`pattern-${value}`} className="rounded bg-primary/15 border border-primary/20 px-2 py-0.5 text-[10px] font-mono text-primary">
            pattern:{value}
          </span>
        ))}
        {rule.match.topics.map((value) => (
          <span key={`topic-${value}`} className="rounded bg-[var(--color-info)]/15 border border-[var(--color-info)]/20 px-2 py-0.5 text-[10px] font-mono text-[var(--color-info)]">
            topic:{value}
          </span>
        ))}
        {rule.match.detectors.length === 0 && rule.match.contentPatterns.length === 0 && rule.match.topics.length === 0 && (
          <span className="text-[10px] font-mono text-muted-foreground italic uppercase tracking-wider">No match criteria defined</span>
        )}
      </div>
    </article>
  );
}
