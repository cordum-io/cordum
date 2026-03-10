import { useState } from "react";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { PolicyEmptyConfigCard } from "@/components/policy/studio-primitives/PolicyEmptyConfigCard";
import { PolicySection } from "@/components/policy/studio-primitives/PolicySection";
import type { GlobalPolicyInputRule } from "@/types/policy";
import { InputRuleCard } from "./InputRuleCard";

interface InputRulesListProps {
  rules: GlobalPolicyInputRule[];
  canEdit: boolean;
  onAddRule: () => void;
  onViewRule: (index: number) => void;
  onEditRule: (index: number) => void;
  onDeleteRule: (index: number) => void;
  onMoveRule: (from: number, to: number) => void;
  onActiveRuleChange?: (ruleId: string | null) => void;
}

export function InputRulesList({
  rules,
  canEdit,
  onAddRule,
  onViewRule,
  onEditRule,
  onDeleteRule,
  onMoveRule,
  onActiveRuleChange,
}: InputRulesListProps) {
  const [announcement, setAnnouncement] = useState("");

  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <p className="text-[11px] text-muted-foreground">
          Ordered rule cards enforce first-match behavior.
        </p>
        {canEdit && (
          <Button
            size="sm"
            onClick={() => {
              onAddRule();
              setAnnouncement("Creating new input rule.");
            }}
          >
            <Plus className="mr-1 h-3.5 w-3.5" />
            Add rule
          </Button>
        )}
      </div>

      <PolicySection title="Input rules" description="Top-to-bottom firewall table for input policy." defaultOpen>
        {rules.length === 0 ? (
          <PolicyEmptyConfigCard
            title="No input rules configured"
            description={
              canEdit
                ? "Add your first rule to move from default-only behavior to explicit first-match controls."
                : "No input rules are configured for the selected bundle."
            }
            ctaLabel={canEdit ? "Add first rule" : undefined}
            onCtaClick={canEdit ? onAddRule : undefined}
          />
        ) : (
          <div className="space-y-3">
            {rules.map((rule, index) => (
              <InputRuleCard
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
