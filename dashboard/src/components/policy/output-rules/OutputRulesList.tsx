import { useState } from "react";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { PolicyEmptyConfigCard } from "@/components/policy/studio-primitives/PolicyEmptyConfigCard";
import { PolicySection } from "@/components/policy/studio-primitives/PolicySection";
import type { GlobalPolicyOutputRule } from "@/types/policy";
import { OutputRuleCard } from "./OutputRuleCard";

interface OutputRulesListProps {
  rules: GlobalPolicyOutputRule[];
  canEdit: boolean;
  onAddRule: () => void;
  onViewRule: (index: number) => void;
  onEditRule: (index: number) => void;
  onDeleteRule: (index: number) => void;
  onToggleRule: (index: number) => void;
  onMoveRule: (from: number, to: number) => void;
  onActiveRuleChange?: (ruleId: string | null) => void;
}

export function OutputRulesList({
  rules,
  canEdit,
  onAddRule,
  onViewRule,
  onEditRule,
  onDeleteRule,
  onToggleRule,
  onMoveRule,
  onActiveRuleChange,
}: OutputRulesListProps) {
  const [announcement, setAnnouncement] = useState("");

  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <p className="text-[11px] text-muted-foreground">
          Output rules are evaluated for scan findings; multiple rules can match.
        </p>
        {canEdit && (
          <Button
            size="sm"
            onClick={() => {
              onAddRule();
              setAnnouncement("Creating new output rule.");
            }}
          >
            <Plus className="mr-1 h-3.5 w-3.5" />
            Add output rule
          </Button>
        )}
      </div>

      <PolicySection title="Output rules" description="Output schema rules for detector findings and delivery decisions." defaultOpen>
        {rules.length === 0 ? (
          <PolicyEmptyConfigCard
            title="No output rules configured"
            description={
              canEdit
                ? "Add your first output rule to define scan finding handling."
                : "No output rules are configured for the selected bundle."
            }
            ctaLabel={canEdit ? "Add first output rule" : undefined}
            onCtaClick={canEdit ? onAddRule : undefined}
          />
        ) : (
          <div className="space-y-3">
            {rules.map((rule, index) => (
              <OutputRuleCard
                key={`${rule.id}-${index}`}
                index={index}
                total={rules.length}
                rule={rule}
                canEdit={canEdit}
                onFocusRule={() => onActiveRuleChange?.(rule.id)}
                onView={() => {
                  onViewRule(index);
                  setAnnouncement(`Viewing ${rule.id}.`);
                }}
                onEdit={() => {
                  onEditRule(index);
                  setAnnouncement(`Editing ${rule.id}.`);
                }}
                onDelete={() => {
                  onDeleteRule(index);
                  setAnnouncement(`Deleted ${rule.id}.`);
                }}
                onToggleEnabled={() => {
                  onToggleRule(index);
                  setAnnouncement(`${rule.id} ${rule.enabled ? "disabled" : "enabled"}.`);
                }}
                onMoveUp={() => {
                  if (index === 0) return;
                  onMoveRule(index, index - 1);
                  setAnnouncement(`Moved ${rule.id} to position ${index}.`);
                }}
                onMoveDown={() => {
                  if (index === rules.length - 1) return;
                  onMoveRule(index, index + 1);
                  setAnnouncement(`Moved ${rule.id} to position ${index + 2}.`);
                }}
              />
            ))}
          </div>
        )}
      </PolicySection>

      <p className="sr-only" role="status" aria-live="polite">
        {announcement}
      </p>
    </section>
  );
}
