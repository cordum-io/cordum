import { StatusBadge } from "@/components/ui/StatusBadge";
import type { GlobalPolicyOutputPolicy, GlobalPolicyOutputRule } from "@/types/policy";

interface OutputPolicyStatusBannerProps {
  outputPolicy: GlobalPolicyOutputPolicy;
  outputRules: GlobalPolicyOutputRule[];
}

export function OutputPolicyStatusBanner({
  outputPolicy,
  outputRules,
}: OutputPolicyStatusBannerProps) {
  const enabledRules = outputRules.filter((rule) => rule.enabled).length;
  const quarantineRules = outputRules.filter((rule) => rule.decision === "quarantine").length;

  return (
    <div className="rounded-lg border border-cordum/20 bg-cordum/10 p-3 text-xs text-cordum-foreground">
      <div className="flex flex-wrap items-center gap-2 mb-2">
        <StatusBadge variant={outputPolicy.enabled ? "healthy" : "muted"}>
          policy {outputPolicy.enabled ? "enabled" : "disabled"}
        </StatusBadge>
        <StatusBadge variant={outputPolicy.failMode === "closed" ? "warning" : "info"}>
          fail_mode: {outputPolicy.failMode}
        </StatusBadge>
        <StatusBadge variant="muted">rules: {outputRules.length}</StatusBadge>
        <StatusBadge variant="muted">enabled: {enabledRules}</StatusBadge>
        <StatusBadge variant="muted">quarantine decisions: {quarantineRules}</StatusBadge>
      </div>
      <p className="text-muted-foreground">
        Output rules are evaluated for scan findings; multiple output rules can match and contribute to final handling behavior.
      </p>
    </div>
  );
}
