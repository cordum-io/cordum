import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight, Sparkles } from "lucide-react";
import { ruleTypeIcon, ruleTypeLabel } from "@/lib/policy-studio/rule-type";
import {
  RULE_TEMPLATES,
  type RuleTemplate,
} from "@/lib/policy-studio/templates";

interface RuleTemplatesGalleryProps {
  onInsert: (template: RuleTemplate) => void;
}

/**
 * Templates gallery embedded inside the Rules editor drawer. Collapsed by
 * default per DoD; opens via a `<details>` disclosure so the empty state of
 * a fresh editor doesn't drown in template choices. Each entry shows the
 * label, a one-line explanation, and the rule type badge so authors can
 * scan-pick. Clicking inserts the template's YAML at the editor cursor (or
 * end-of-document when unfocused) via the parent's `onInsert` callback.
 */
export function RuleTemplatesGallery({ onInsert }: RuleTemplatesGalleryProps) {
  const [open, setOpen] = useState(false);
  const grouped = useMemo(() => groupTemplatesByType(RULE_TEMPLATES), []);

  return (
    <details
      open={open}
      onToggle={(event) => setOpen((event.target as HTMLDetailsElement).open)}
      className="rounded-2xl border border-border bg-surface-1"
    >
      <summary
        className="flex cursor-pointer items-center gap-2 px-3 py-2 text-xs font-mono uppercase tracking-wider text-muted-foreground"
        aria-label="Toggle rule templates gallery"
      >
        {open ? (
          <ChevronDown aria-hidden className="h-3.5 w-3.5" />
        ) : (
          <ChevronRight aria-hidden className="h-3.5 w-3.5" />
        )}
        <Sparkles aria-hidden className="h-3.5 w-3.5" />
        Templates
        <span className="ml-1 rounded-full bg-surface-2 px-2 py-0.5 text-[0.6rem] tracking-wide">
          {RULE_TEMPLATES.length}
        </span>
      </summary>

      <ul className="space-y-3 px-3 pb-3 pt-1">
        {grouped.map(({ ruleType, templates }) => (
          <li key={ruleType}>
            <p className="mb-1 text-[0.65rem] font-mono uppercase tracking-wider text-muted-foreground">
              {ruleTypeLabel(ruleType)}
            </p>
            <ul className="grid gap-2 sm:grid-cols-2">
              {templates.map((template) => (
                <li key={template.id}>
                  <TemplateButton template={template} onInsert={onInsert} />
                </li>
              ))}
            </ul>
          </li>
        ))}
      </ul>
    </details>
  );
}

function TemplateButton({
  template,
  onInsert,
}: {
  template: RuleTemplate;
  onInsert: (template: RuleTemplate) => void;
}) {
  const Icon = ruleTypeIcon(template.ruleType);
  return (
    <button
      type="button"
      data-template-id={template.id}
      onClick={() => onInsert(template)}
      className="flex w-full items-start gap-2 rounded-xl border border-border bg-surface-2 px-3 py-2 text-left text-xs hover:border-cordum/60 hover:bg-cordum/5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cordum/60"
      aria-label={`Insert template: ${template.label}`}
    >
      <Icon
        aria-hidden
        className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-muted-foreground"
      />
      <span className="flex flex-col gap-0.5">
        <span className="font-medium text-foreground">{template.label}</span>
        <span className="text-muted-foreground">{template.description}</span>
      </span>
    </button>
  );
}

interface GroupedTemplates {
  ruleType: RuleTemplate["ruleType"];
  templates: RuleTemplate[];
}

function groupTemplatesByType(
  templates: ReadonlyArray<RuleTemplate>,
): GroupedTemplates[] {
  const order: RuleTemplate["ruleType"][] = [];
  const buckets = new Map<RuleTemplate["ruleType"], RuleTemplate[]>();
  for (const template of templates) {
    if (!buckets.has(template.ruleType)) {
      buckets.set(template.ruleType, []);
      order.push(template.ruleType);
    }
    buckets.get(template.ruleType)!.push(template);
  }
  return order.map((ruleType) => ({
    ruleType,
    templates: buckets.get(ruleType)!,
  }));
}
