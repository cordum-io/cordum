import type { GlobalPolicyInputRule } from "@/types/policy";

function hasMcpConfiguration(rule: GlobalPolicyInputRule): boolean {
  const mcp = rule.match.mcp;
  return Boolean(
    mcp.allowServers.length
      || mcp.denyServers.length
      || mcp.allowTools.length
      || mcp.denyTools.length
      || mcp.allowResources.length
      || mcp.denyResources.length
      || mcp.allowActions.length
      || mcp.denyActions.length,
  );
}

function hasConstraintsConfiguration(rule: GlobalPolicyInputRule): boolean {
  const { budgets, sandbox, toolchain, diff, redactionLevel } = rule.constraints;
  return Boolean(
    budgets.maxRuntimeMs
      || budgets.maxRetries
      || budgets.maxArtifactBytes
      || budgets.maxConcurrentJobs
      || sandbox.isolated
      || sandbox.networkAllowlist.length
      || sandbox.fsReadOnly.length
      || sandbox.fsReadWrite.length
      || toolchain.allowedTools.length
      || toolchain.allowedCommands.length
      || diff.maxFiles
      || diff.maxLines
      || diff.denyPathGlobs.length
      || redactionLevel?.trim(),
  );
}

export interface AdvancedConfiguredGroups {
  actorIds: boolean;
  labels: boolean;
  mcp: boolean;
  constraints: boolean;
  remediations: boolean;
}

export interface AdvancedConfiguredSummary {
  count: number;
  groups: AdvancedConfiguredGroups;
}

export function getAdvancedConfiguredSummary(
  rule: GlobalPolicyInputRule,
): AdvancedConfiguredSummary {
  const constraintsConfigured = hasConstraintsConfiguration(rule);
  const remediationsConfigured = rule.remediations.length > 0;
  const groups: AdvancedConfiguredGroups = {
    actorIds: rule.match.actorIds.length > 0,
    labels: Object.keys(rule.match.labels).length > 0,
    mcp: hasMcpConfiguration(rule),
    constraints: rule.decision !== "allow_with_constraints" && constraintsConfigured,
    remediations: rule.decision !== "deny" && remediationsConfigured,
  };

  const count = Object.values(groups).filter(Boolean).length;
  return { count, groups };
}

export const __globalRuleEditorStateInternal = {
  hasMcpConfiguration,
  hasConstraintsConfiguration,
};
