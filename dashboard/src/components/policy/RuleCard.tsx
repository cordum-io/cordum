import { useState, useCallback } from "react";
import { GripVertical, Pencil, Trash2, AlertTriangle, Zap } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { cn } from "../../lib/utils";
import { formatCount } from "../../lib/format";
import type { PolicyRule } from "../../api/types";

const decisionStyles: Record<string, { label: string; variant: "success" | "governance" | "warning" | "info" }> = {
  allow: { label: "Allow", variant: "success" },
  deny: { label: "Deny", variant: "governance" },
  require_approval: { label: "Require Approval", variant: "warning" },
  throttle: { label: "Throttle", variant: "info" },
};

function MiniSparkline({ value }: { value: number }) {
  // Simple bar representation of activity
  const bars = 7;
  const max = Math.max(value, 1);
  return (
    <span className="inline-flex items-end gap-px">
      {Array.from({ length: bars }, (_, i) => {
        const h = Math.max(2, Math.round(((i + 1) / bars) * 12 * (value / max)));
        return (
          <span
            key={i}
            className="inline-block w-[3px] rounded-sm bg-accent/40"
            style={{ height: `${h}px` }}
          />
        );
      })}
    </span>
  );
}

interface RuleCardProps {
  rule: PolicyRule;
  index: number;
  conflictWarning?: string;
  onEdit: () => void;
  onDelete: () => void;
  onToggleEnabled?: (ruleId: string, enabled: boolean) => void;
  onTest?: (rule: PolicyRule) => void;
  onDragStart: (e: React.DragEvent, index: number) => void;
  onDragOver: (e: React.DragEvent) => void;
  onDrop: (e: React.DragEvent, index: number) => void;
}

export function RuleCard({
  rule,
  index,
  conflictWarning,
  onEdit,
  onDelete,
  onToggleEnabled,
  onTest,
  onDragStart,
  onDragOver,
  onDrop,
}: RuleCardProps) {
  const [confirmDelete, setConfirmDelete] = useState(false);
  const decision = decisionStyles[rule.decisionType ?? "allow"] ?? decisionStyles.allow;
  const isDisabled = rule.enabled === false;

  const handleToggle = useCallback(() => {
    if (!onToggleEnabled) return;
    if (!isDisabled) {
      if (!window.confirm("Disable this rule? It will stop being evaluated.")) return;
    }
    onToggleEnabled(rule.id, isDisabled);
  }, [onToggleEnabled, rule.id, isDisabled]);

  const capabilities = (rule.matchCriteria?.capabilities as string[] | undefined) ?? [];
  const riskTags = (rule.matchCriteria?.riskTags as string[] | undefined) ?? [];
  const logic = rule.logic === "OR" ? "any of" : "all of";
  const hasMatch = capabilities.length > 0 || riskTags.length > 0;

  return (
    <div
      className={cn(
        "list-row flex items-start gap-3 animate-rise",
        conflictWarning && "border-warning/40",
        isDisabled && "opacity-50",
      )}
      draggable
      onDragStart={(e) => onDragStart(e, index)}
      onDragOver={onDragOver}
      onDrop={(e) => onDrop(e, index)}
    >
      {/* Drag handle */}
      <button
        type="button"
        className="mt-1 cursor-grab text-muted/50 hover:text-muted-foreground active:cursor-grabbing"
        aria-label="Drag to reorder"
      >
        <GripVertical className="h-4 w-4" />
      </button>

      {/* Toggle switch */}
      {onToggleEnabled && (
        <button
          type="button"
          role="switch"
          aria-checked={!isDisabled}
          aria-label={isDisabled ? "Enable rule" : "Disable rule"}
          className={cn(
            "mt-1 relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors",
            isDisabled ? "bg-muted/30" : "bg-accent",
          )}
          onClick={handleToggle}
        >
          <span
            className={cn(
              "pointer-events-none inline-block h-4 w-4 rounded-full bg-card shadow transition-transform",
              isDisabled ? "translate-x-0" : "translate-x-4",
            )}
          />
        </button>
      )}

      {/* Priority number */}
      <span className="mt-0.5 flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-lg bg-accent/10 text-xs font-bold text-accent">
        {index + 1}
      </span>

      {/* Disabled badge */}
      {isDisabled && (
        <Badge variant="warning" className="mt-0.5 text-xs">Disabled</Badge>
      )}

      {/* Rule sentence */}
      <div className="flex-1 space-y-2">
        <p className="text-sm leading-relaxed text-ink">
          <span className="text-muted-foreground">When a job has </span>
          {hasMatch ? (
            <>
              <span className="text-muted-foreground">{logic} </span>
              {capabilities.length > 0 && (
                <>
                  <span className="text-muted-foreground">capabilities </span>
                  {capabilities.map((cap) => (
                    <Badge key={cap} variant="info" className="mx-0.5">
                      {cap}
                    </Badge>
                  ))}
                </>
              )}
              {capabilities.length > 0 && riskTags.length > 0 && (
                <span className="text-muted-foreground"> and </span>
              )}
              {riskTags.length > 0 && (
                <>
                  <span className="text-muted-foreground">risk tags </span>
                  {riskTags.map((tag) => (
                    <Badge key={tag} variant="danger" className="mx-0.5">
                      {tag}
                    </Badge>
                  ))}
                </>
              )}
            </>
          ) : (
            <span className="text-muted-foreground">any match criteria </span>
          )}
          <span className="text-muted-foreground"> then </span>
          <Badge variant={decision.variant} className="mx-0.5 text-sm">
            {decision.label}
          </Badge>
          {rule.reason && (
            <>
              <span className="text-muted-foreground"> because </span>
              <span className="italic text-ink/80">{rule.reason}</span>
            </>
          )}
        </p>

        {/* Inline stats */}
        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          {rule.hitCount24h !== undefined && (
            <span className="flex items-center gap-1.5">
              <MiniSparkline value={rule.hitCount24h} />
              triggered {formatCount(rule.hitCount24h)} times in 24h
            </span>
          )}
          {rule.lastTriggered && (
            <span>
              last: {new Date(rule.lastTriggered).toLocaleString()}
            </span>
          )}
        </div>

        {/* Conflict warning */}
        {conflictWarning && (
          <div className="flex items-center gap-1.5 text-xs text-warning">
            <AlertTriangle className="h-3 w-3" />
            {conflictWarning}
          </div>
        )}
      </div>

      {/* Actions */}
      <div className="flex flex-shrink-0 gap-1">
        {onTest && (
          <Button variant="ghost" size="sm" type="button" onClick={() => onTest(rule)} title="Test this rule">
            <Zap className="h-3.5 w-3.5" />
          </Button>
        )}
        <Button variant="ghost" size="sm" type="button" onClick={onEdit}>
          <Pencil className="h-3.5 w-3.5" />
        </Button>
        {confirmDelete ? (
          <div className="flex items-center gap-1">
            <Button variant="danger" size="sm" type="button" onClick={onDelete}>
              Confirm
            </Button>
            <Button variant="ghost" size="sm" type="button" onClick={() => setConfirmDelete(false)}>
              Cancel
            </Button>
          </div>
        ) : (
          <Button variant="ghost" size="sm" type="button" aria-label="Delete rule" onClick={() => setConfirmDelete(true)}>
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        )}
      </div>
    </div>
  );
}
