import { Plus, Shield } from "lucide-react";
import { Link } from "react-router-dom";
import { EmptyState } from "@/components/ui/EmptyState";
import { useBundle } from "@/hooks/useBundle";

interface BundleRulesTabProps {
  bundleId: string;
}

// Cross-link (D10a #7) URL contract per spec § "Cross-links are the
// load-bearing wall": Bundles "Add rule" → Rules with bundle pre-bound.
// The Rules surface drawer reads the `bundle` query param (D3E publish-
// to-bundle wires the actual pre-binding once that lands); the link's
// destination is canonical today and the param will be honored when D3E
// ships. See `dashboard/docs/policy-studio-editor.md` cross-link section.
function addRuleHref(bundleId: string): string {
  return `/policies?rule=new&open=editor&type=input&bundle=${encodeURIComponent(bundleId)}`;
}

/**
 * Bundle detail — Rules tab (Dashboard 5 step 4c).
 *
 * Renders the rule list bound to the bundle's `rule_ids` array. The
 * unified Bundle shape stores rules by id (with the at-deploy snapshot
 * inside `versions[].rule_snapshot`); this tab queries the rule store
 * for live metadata. Until Dashboard 2 ships PoliciesPage's RuleRow
 * primitive, this tab renders a minimal id list — once RuleRow is
 * extracted to `src/components/policy-studio/`, this consumer migrates
 * to it (DIRECTIVE rail satisfied: 2+ consumers).
 */
export default function BundleRulesTab({ bundleId }: BundleRulesTabProps) {
  const { data: bundle, isPending } = useBundle(bundleId);
  const ruleIds = bundle?.rule_ids ?? [];

  if (isPending) {
    return (
      <div className="text-sm text-muted-foreground py-6 text-center">
        Loading rules…
      </div>
    );
  }

  // Anchor styled like a Button — see `Button.tsx` for tokens. Avoids the
  // Button primitive's <button> tag (it doesn't support `asChild`); the
  // anchor satisfies the SHARED CODE FIRST rail by reusing the same token
  // set + variants without forking the primitive.
  const addRuleClass =
    "inline-flex items-center justify-center font-medium transition-all duration-[var(--duration-soft)] ease-out whitespace-nowrap " +
    "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cordum/40 focus-visible:ring-offset-2 focus-visible:ring-offset-background " +
    "active:scale-[0.98] hover:-translate-y-[1px] hover:shadow-soft-hover h-8 px-3 text-xs rounded-xl gap-1.5";

  if (ruleIds.length === 0) {
    return (
      <div className="space-y-3">
        <EmptyState
          icon={<Shield className="h-5 w-5" />}
          title="No rules in this bundle"
          description="Add rules from the Rules surface or create new ones bound to this bundle."
        />
        <div className="flex justify-center">
          <Link
            to={addRuleHref(bundleId)}
            data-row-action="cross-link-add-rule"
            aria-label={`Add a rule to bundle ${bundleId}`}
            className={`${addRuleClass} bg-primary text-primary-foreground hover:bg-primary/85 font-semibold shadow-glow`}
          >
            <Plus className="mr-1 h-3.5 w-3.5" aria-hidden />
            Add rule…
          </Link>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <ul className="divide-y divide-border rounded-2xl border border-border bg-surface-1 overflow-hidden">
        {ruleIds.map((ruleId) => (
          <li
            key={ruleId}
            className="flex items-center gap-3 px-4 py-3 text-sm hover:bg-surface-2/40 transition-colors"
          >
            <Shield className="h-4 w-4 text-muted-foreground shrink-0" aria-hidden />
            <span className="font-mono text-foreground">{ruleId}</span>
          </li>
        ))}
      </ul>
      <div className="flex justify-end">
        <Link
          to={addRuleHref(bundleId)}
          data-row-action="cross-link-add-rule"
          aria-label={`Add another rule to bundle ${bundleId}`}
          className={`${addRuleClass} text-muted-foreground hover:text-foreground hover:bg-secondary`}
        >
          <Plus className="mr-1 h-3.5 w-3.5" aria-hidden />
          Add rule…
        </Link>
      </div>
    </div>
  );
}
