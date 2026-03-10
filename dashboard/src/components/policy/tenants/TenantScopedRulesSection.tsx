import type { GlobalPolicyInputRule } from "@/types/policy";

interface TenantScopedRulesSectionProps {
  tenantId: string;
  rules: GlobalPolicyInputRule[];
}

function decisionTone(decision: GlobalPolicyInputRule["decision"]): string {
  switch (decision) {
    case "allow":
      return "bg-[var(--color-success)]/20 text-[var(--color-success)]";
    case "deny":
      return "bg-destructive/20 text-destructive";
    case "allow_with_constraints":
      return "bg-[var(--color-info)]/20 text-[var(--color-info)]";
    case "throttle":
      return "bg-[var(--color-warning)]/20 text-[var(--color-warning)]";
    default:
      return "bg-[var(--color-warning)]/20 text-[var(--color-warning)]";
  }
}

export function TenantScopedRulesSection({
  tenantId,
  rules,
}: TenantScopedRulesSectionProps) {
  return (
    <section className="rounded-lg border border-border bg-surface-0 p-4 space-y-3">
      <div>
        <h3 className="font-display text-sm font-semibold text-foreground">
          Tenant-Scoped Rules
        </h3>
        <p className="mt-1 text-xs text-muted-foreground">
          Input rules that explicitly match <span className="font-mono text-foreground">{tenantId}</span> in{" "}
          <span className="font-mono text-foreground">match.tenants</span>. These rules are merged with global policy at evaluation time.
        </p>
      </div>

      {rules.length === 0 ? (
        <div className="rounded border border-border/70 bg-surface-1 p-3 text-xs text-muted-foreground">
          No tenant-scoped input rules currently reference this tenant.
        </div>
      ) : (
        <div className="space-y-2">
          {rules.map((rule, index) => (
            <article key={`${rule.id}-${index}`} className="rounded border border-border/70 bg-surface-1 p-3">
              <div className="flex flex-wrap items-center gap-2">
                <span className="rounded bg-surface-2 px-2 py-0.5 text-[10px] font-mono text-muted-foreground">
                  {rule.id}
                </span>
                <span className={`rounded px-2 py-0.5 text-[10px] font-mono uppercase ${decisionTone(rule.decision)}`}>
                  {rule.decision}
                </span>
              </div>
              {rule.reason.trim() && (
                <p className="mt-2 text-xs text-muted-foreground">{rule.reason.trim()}</p>
              )}
            </article>
          ))}
        </div>
      )}
    </section>
  );
}
