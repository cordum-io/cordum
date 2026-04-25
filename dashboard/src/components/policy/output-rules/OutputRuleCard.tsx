import { ArrowDown, ArrowUp, Eye, Pencil, Power, Trash2, Target, Shield, AlertTriangle, Fingerprint } from "lucide-react";
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

function MatchItem({ icon: Icon, label, value, variant = "default" }: { icon: any, label: string, value: string[], variant?: "default" | "warning" | "info" | "primary" }) {
  if (!value || value.length === 0) return null;
  
  const variantClasses = {
    default: "bg-surface-0/60 border-border/10",
    warning: "bg-[var(--color-warning)]/10 border-[var(--color-warning)]/20 text-[var(--color-warning)]",
    info: "bg-[var(--color-info)]/10 border-[var(--color-info)]/20 text-[var(--color-info)]",
    primary: "bg-primary/10 border-primary/20 text-primary",
  };

  return (
    <div className="flex items-start gap-2 text-[11px]">
      <Icon className="w-3 h-3 text-muted-foreground mt-0.5 shrink-0" />
      <div className="min-w-0">
        <span className="text-muted-foreground font-mono uppercase tracking-wider text-[9px] block leading-none mb-1">{label}</span>
        <div className="flex flex-wrap gap-1">
          {value.map((v) => (
            <span key={v} className={cn("border rounded px-1.5 py-0.5 font-mono truncate max-w-[200px]", variantClasses[variant])} title={v}>
              {v}
            </span>
          ))}
        </div>
      </div>
    </div>
  );
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
  const m = rule.match;

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
      className="instrument-card p-0 overflow-hidden outline-none focus:ring-2 focus:ring-cordum/40 group hover:shadow-soft-hover transition-all border border-border/50"
    >
      {/* Header — Identity & Actions */}
      <header className="flex items-center justify-between px-4 py-2.5 border-b border-border/30 bg-surface-1/50">
        <div className="flex items-center gap-3">
          <div className="flex items-center justify-center w-6 h-6 rounded-lg bg-surface-2 text-[10px] font-mono font-bold text-muted-foreground border border-border/10">
            {index + 1}
          </div>
          <div>
            <h3 className="text-xs font-semibold text-foreground tracking-tight">{rule.id}</h3>
            {rule.description && <p className="text-[10px] text-muted-foreground leading-none mt-0.5">{rule.description}</p>}
          </div>
        </div>

        <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 transition-opacity">
          {canEdit && (
            <>
              <button type="button"
                className="p-1.5 rounded-lg text-muted-foreground hover:bg-surface-2 transition-colors"
                onClick={onToggleEnabled}
                title={rule.enabled ? "Disable Rule" : "Enable Rule"}
              >
                <Power className="h-3.5 w-3.5" />
              </button>
              <button type="button"
                className="p-1.5 rounded-lg text-muted-foreground hover:bg-surface-2 disabled:opacity-40 transition-colors"
                disabled={index === 0}
                onClick={onMoveUp}
                title="Move Up (Alt + Up)"
              >
                <ArrowUp className="h-3.5 w-3.5" />
              </button>
              <button type="button"
                className="p-1.5 rounded-lg text-muted-foreground hover:bg-surface-2 disabled:opacity-40 transition-colors"
                disabled={index === total - 1}
                onClick={onMoveDown}
                title="Move Down (Alt + Down)"
              >
                <ArrowDown className="h-3.5 w-3.5" />
              </button>
              <div className="w-px h-3 bg-border/40 mx-1" />
              <button type="button" 
                className="p-1.5 rounded-lg text-muted-foreground hover:bg-surface-2 transition-colors" 
                onClick={onEdit}
                title="Edit Rule"
              >
                <Pencil className="h-3.5 w-3.5" />
              </button>
              <button type="button" 
                className="p-1.5 rounded-lg text-destructive hover:bg-destructive/10 transition-colors"
                onClick={onDelete}
                title="Delete Rule"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            </>
          )}
          {!canEdit && (
            <button type="button" 
              className="p-1.5 rounded-lg text-muted-foreground hover:bg-surface-2 transition-colors" 
              onClick={onView}
              title="View Rule"
            >
              <Eye className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      </header>

      {/* Visual Logic Flow */}
      <div className="flex flex-col md:flex-row divide-y md:divide-y-0 md:divide-x divide-border/20">
        {/* IF Part */}
        <div className="flex-1 p-4 bg-surface-0/20">
          <div className="flex items-center gap-2 mb-3">
            <div className="px-1.5 py-0.5 rounded-md bg-muted text-[9px] font-bold text-muted-foreground uppercase tracking-widest border border-border/10">IF</div>
            <span className="text-[10px] text-muted-foreground font-medium">OUTPUT MATCHES</span>
          </div>
          
          <div className="grid grid-cols-1 gap-4">
            <MatchItem icon={AlertTriangle} label="Detectors" value={m.detectors} variant="warning" />
            <MatchItem icon={Fingerprint} label="Content Patterns" value={m.contentPatterns} variant="primary" />
            <MatchItem icon={Target} label="Topics" value={m.topics} variant="info" />
            {!m.detectors.length && !m.contentPatterns.length && !m.topics.length && (
              <p className="text-[11px] text-muted-foreground italic pl-5">No specific filters (matches all findings)</p>
            )}
          </div>
        </div>

        {/* EMITS Part */}
        <div className="md:w-48 p-4 bg-surface-1/30 flex flex-col justify-center">
          <div className="flex items-center gap-2 mb-3">
            <div className="px-1.5 py-0.5 rounded-md bg-cordum/10 text-[9px] font-bold text-cordum uppercase tracking-widest border border-cordum/20">EMIT</div>
            <span className="text-[10px] text-muted-foreground font-medium">DECISION</span>
          </div>

          <div className="flex flex-col gap-3 items-start">
            <div className="flex flex-col gap-1.5 w-full">
              <SafetyDecisionBadge decision={rule.decision} />
              <span className={cn("inline-flex items-center px-2 py-0.5 rounded text-[10px] font-mono font-bold uppercase border w-fit", severityClass(rule.severity))}>
                {rule.severity}
              </span>
            </div>
            
            <div className="flex items-center gap-1.5 p-2 rounded-xl bg-surface-2/50 border border-border/10 w-full">
              <Shield className="w-3 h-3 text-cordum" />
              <div className="min-w-0">
                <p className="text-[9px] font-mono text-foreground font-medium leading-tight">Evaluation</p>
                <p className="text-[8px] text-muted-foreground font-mono truncate">active_rule:true</p>
              </div>
            </div>
          </div>
        </div>
      </div>
    </article>
  );
}
