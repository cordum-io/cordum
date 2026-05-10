import type { Rule } from "@/api/generated/model/rule";
import { RuleType } from "@/api/generated/model/ruleType";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";

const FIXED_AT = "2026-05-10T12:00:00Z";

/**
 * Canonical Rule fixture for Backend 5c write-API tests. Two rows: one
 * tenant-scoped input rule (typical "block secret" pattern), one
 * global-scoped edge rule (typical "deny destructive shell" pattern).
 * Both ship with server-set Version=v1 + Audit + Status=draft so the
 * dashboard's PUT-flow reload-banner tests can drive a stale-409 path
 * by mutating one server-side and replaying with the original version.
 */
export const fixturePolicyRules: Rule[] = [
  {
    id: "rule.input.secret-scan",
    name: "Block secrets in input",
    type: RuleType.input,
    scope: { kind: RuleScopeKind.tenant, value: "tenant-acme" },
    status: RuleStatus.draft,
    version: "v1",
    audit: {
      created_at: FIXED_AT,
      updated_at: FIXED_AT,
      created_by: "alice",
      updated_by: "alice",
    },
    match: {
      topics: ["job.acme.evaluate"],
      keywords: ["aws-access-key", "secret"],
    },
    decide: {
      decision: "deny",
      reason: "secret pattern matched",
    },
    description: "Tenant-scoped input rule for the secret-scan demo.",
  },
  {
    id: "rule.edge.deny-destructive",
    name: "Deny destructive shell",
    type: RuleType.edge,
    scope: { kind: RuleScopeKind.global },
    status: RuleStatus.draft,
    version: "v1",
    audit: {
      created_at: FIXED_AT,
      updated_at: FIXED_AT,
      created_by: "alice",
      updated_by: "alice",
    },
    match: {
      topics: ["edge.agent_action"],
      capabilities: ["exec.shell"],
      risk_tags: ["destructive"],
    },
    decide: {
      decision: "deny",
      reason: "destructive shell denied",
    },
    description: "Global edge rule for the destructive-shell demo.",
  },
];
