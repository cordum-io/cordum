import { Link } from "react-router-dom";
import { Sparkles } from "lucide-react";
import { ruleTypeIcon, ruleTypeLabel } from "@/lib/policy-studio/rule-type";
import {
  RULE_TEMPLATES,
  type RuleTemplate,
} from "@/lib/policy-studio/templates";

/**
 * Empty-state templates gallery rendered on PoliciesPage when the rules
 * list is genuinely empty (no rows, no filters). This is the standalone
 * full-width grid version — different from RuleTemplatesGallery, which is
 * the in-drawer collapsed disclosure used by RuleEditorDrawer.
 *
 * UX contract:
 * - Responsive 3-col grid above a `sm` breakpoint, single-col below.
 * - Each card is a router-aware Link, not a button — clicking opens the
 *   editor at `/policies?new=true&type=<ruleType>&template=<id>&open=editor`.
 *   The drawer's URL handling reads `template=` and pre-fills Monaco from
 *   the corresponding YAML stub. `new=true` is the alternate-form of
 *   `rule=new` (3A's create-new path) — both routes seed a draft.
 * - Each card surfaces the rule-type badge + title + 1-2 line description
 *   so authors can scan-pick before clicking through.
 *
 * Filtered-empty state (search/scope/status filters with zero matches)
 * does NOT render this gallery; the parent EmptyState branches on
 * `filtersActive` and only mounts the gallery when filters are absent.
 */
export function PoliciesEmptyTemplatesGallery() {
  return (
    <section
      aria-labelledby="policies-empty-templates-heading"
      className="rounded-2xl border border-border bg-surface-1 p-4"
    >
      <header className="mb-4 flex items-center gap-2">
        <Sparkles aria-hidden className="h-4 w-4 text-cordum" />
        <h3
          id="policies-empty-templates-heading"
          className="text-sm font-semibold text-foreground"
        >
          Start from a template
        </h3>
        <span className="ml-1 rounded-full bg-surface-2 px-2 py-0.5 text-[0.6rem] font-mono uppercase tracking-wider text-muted-foreground">
          {RULE_TEMPLATES.length}
        </span>
      </header>
      <ul
        className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3"
        role="list"
      >
        {RULE_TEMPLATES.map((template) => (
          <li key={template.id}>
            <PoliciesEmptyTemplateCard template={template} />
          </li>
        ))}
      </ul>
    </section>
  );
}

function PoliciesEmptyTemplateCard({ template }: { template: RuleTemplate }) {
  const Icon = ruleTypeIcon(template.ruleType);
  const typeLabel = ruleTypeLabel(template.ruleType);
  // The deep-link contract is documented in cordum/dashboard/docs/policy-studio-editor.md
  // Cross-link contract section. Drawer reads `template=` and pre-fills the
  // Monaco editor from RULE_TEMPLATES.find(t => t.id === id).yaml.
  const href = `/policies?new=true&type=${encodeURIComponent(template.ruleType)}&template=${encodeURIComponent(template.id)}&open=editor`;
  return (
    <Link
      to={href}
      data-template-id={template.id}
      data-row-action
      className="flex h-full flex-col gap-2 rounded-xl border border-border bg-surface-2 p-3 text-left text-xs transition-colors hover:border-cordum/60 hover:bg-cordum/5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cordum/60"
      aria-label={`Use template: ${template.label} (${typeLabel})`}
    >
      <span className="flex items-center gap-2">
        <Icon
          aria-hidden
          className="h-3.5 w-3.5 flex-shrink-0 text-muted-foreground"
        />
        <span className="font-mono text-[0.65rem] uppercase tracking-wider text-muted-foreground">
          {typeLabel}
        </span>
      </span>
      <span className="font-medium text-foreground">{template.label}</span>
      <span className="text-muted-foreground">{template.description}</span>
    </Link>
  );
}

export default PoliciesEmptyTemplatesGallery;
