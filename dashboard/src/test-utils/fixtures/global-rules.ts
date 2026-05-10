import type { GlobalPolicyInputRule, GlobalPolicyOutputRule } from "@/types/policy";

export function createEmptyGlobalInputRule(nextIndex = 1): GlobalPolicyInputRule {
  return {
    id: `rule-${nextIndex}`,
    decision: "deny",
    reason: "",
    match: {
      tenants: [],
      topics: [],
      capabilities: [],
      riskTags: [],
      requires: [],
      packIds: [],
      actorIds: [],
      actorTypes: [],
      labels: {},
      secretsPresent: null,
      mcp: {
        allowServers: [],
        denyServers: [],
        allowTools: [],
        denyTools: [],
        allowResources: [],
        denyResources: [],
        allowActions: [],
        denyActions: [],
      },
    },
    constraints: {
      budgets: {},
      sandbox: { networkAllowlist: [], fsReadOnly: [], fsReadWrite: [] },
      toolchain: { allowedTools: [], allowedCommands: [] },
      diff: { denyPathGlobs: [] },
    },
    remediations: [],
    source: {},
  };
}

export function createEmptyGlobalOutputRule(nextIndex = 1): GlobalPolicyOutputRule {
  return {
    id: `output-rule-${nextIndex}`,
    enabled: true,
    severity: "medium",
    description: "",
    decision: "quarantine",
    reason: "",
    match: {
      tenants: [],
      topics: [],
      capabilities: [],
      riskTags: [],
      scanners: [],
      contentPatterns: [],
      keywords: [],
      contentTypes: [],
      detectors: [],
      outputSizeGt: undefined,
      maxOutputBytes: undefined,
      hasError: null,
    },
    source: {},
  };
}
