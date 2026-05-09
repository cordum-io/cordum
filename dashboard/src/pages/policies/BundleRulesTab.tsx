import { Shield } from "lucide-react";
import { EmptyState } from "@/components/ui/EmptyState";
import { useBundle } from "@/hooks/useBundle";

interface BundleRulesTabProps {
  bundleId: string;
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

  if (ruleIds.length === 0) {
    return (
      <EmptyState
        icon={<Shield className="h-5 w-5" />}
        title="No rules in this bundle"
        description="Add rules from the Rules surface or create new ones bound to this bundle."
      />
    );
  }

  return (
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
  );
}
