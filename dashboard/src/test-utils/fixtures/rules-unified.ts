import type { Rule } from "@/api/generated/model/rule";
import { RuleType } from "@/api/generated/model/ruleType";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";

const FIXED_AT = "2026-05-10T12:00:00Z";

function pack(packID: string, overlay = "safety"): Rule["audit"]["pack_source"] {
  return {
    fragment_id: `${packID}/${overlay}`,
    pack_id: packID,
    overlay_name: overlay,
    tier: "global",
    version: "1.0.0",
    installed_at: "2026-05-09T10:00:00Z",
    sha256: `sha256:${packID}-${overlay}`,
  };
}

/**
 * 8-row Rule fixture mirroring the dev-stack data shape Yaron observed
 * (cordclaw/openclaw/visa/b2b/claude-code packs). Every row has
 * `type` populated so the dashboard's `ruleTypeIcon(rule.type)` no
 * longer falls back to "Unknown" — that's the bug Backend 5d fixes.
 *
 * Distribution: 3 input, 2 output, 2 velocity, 1 edge.
 * Scopes: 5 global, 2 tenant, 1 workflow.
 * Statuses: 6 published, 2 draft.
 */
export const fixtureUnifiedRules: Rule[] = [
  {
    id: "cordclaw.input.aws-secret",
    name: "Block AWS access keys in prompts",
    type: RuleType.input,
    scope: { kind: RuleScopeKind.global },
    status: RuleStatus.published,
    version: "v1",
    audit: { created_at: FIXED_AT, created_by: "cordclaw", pack_source: pack("cordclaw") },
    match: { topics: ["job.*"], keywords: ["AKIA"] },
    decide: { decision: "deny", reason: "AWS access key prefix matched" },
    description: "Cordclaw safety pack — blocks AWS access keys in agent input.",
  },
  {
    id: "openclaw.input.shell-injection",
    name: "Block shell injection in tool args",
    type: RuleType.input,
    scope: { kind: RuleScopeKind.tenant, value: "tenant-acme" },
    status: RuleStatus.published,
    version: "v1",
    audit: { created_at: FIXED_AT, created_by: "openclaw", pack_source: pack("openclaw") },
    match: { topics: ["tool.shell.*"], keywords: ["$(", "`", ";"] },
    decide: { decision: "deny", reason: "shell metacharacter detected" },
    description: "OpenClaw safety pack — denies shell-meta injection.",
  },
  {
    id: "visa.input.cardholder-pii",
    name: "Block cardholder PII",
    type: RuleType.input,
    scope: { kind: RuleScopeKind.global },
    status: RuleStatus.draft,
    version: "v1",
    audit: { created_at: FIXED_AT, created_by: "visa", pack_source: pack("visa", "pci") },
    match: { detectors: ["cardholder-pii"] },
    decide: { decision: "redact", reason: "PCI cardholder data" },
    description: "Visa PCI pack — redacts cardholder PII before model dispatch.",
  },
  {
    id: "cordclaw.output.secret-leak",
    name: "Quarantine secret leaks in output",
    type: RuleType.output,
    scope: { kind: RuleScopeKind.global },
    status: RuleStatus.published,
    version: "v1",
    audit: { created_at: FIXED_AT, created_by: "cordclaw", pack_source: pack("cordclaw") },
    match: { scanners: ["secret"], content_patterns: ["AKIA[0-9A-Z]{16}"] },
    decide: { decision: "quarantine", reason: "secret detected in model output", severity: "high" },
    description: "Cordclaw output guard — quarantines suspected secret leaks.",
  },
  {
    id: "b2b.output.pii-redact",
    name: "Redact PII in output",
    type: RuleType.output,
    scope: { kind: RuleScopeKind.tenant, value: "tenant-acme" },
    status: RuleStatus.published,
    version: "v1",
    audit: { created_at: FIXED_AT, created_by: "b2b", pack_source: pack("b2b") },
    match: { detectors: ["pii"] },
    decide: { decision: "redact", reason: "pii", severity: "medium" },
    description: "B2B pack — redacts PII before downstream model output dispatch.",
  },
  {
    id: "claude-code.velocity.ingest-rate",
    name: "Throttle ingest topic velocity",
    type: RuleType.velocity,
    scope: { kind: RuleScopeKind.workflow, value: "ingest-pipeline" },
    status: RuleStatus.published,
    version: "v1",
    audit: { created_at: FIXED_AT, created_by: "claude-code", pack_source: pack("claude-code") },
    match: {
      topics: ["job.ingest"],
      velocity: { window: "60s", key: "tenant_id", threshold: 100 },
    },
    decide: { decision: "throttle", reason: "ingest rate exceeded 100 req/min" },
    description: "Claude Code pack — caps ingest velocity per tenant.",
  },
  {
    id: "openclaw.velocity.tool-spam",
    name: "Throttle tool-call spam",
    type: RuleType.velocity,
    scope: { kind: RuleScopeKind.global },
    status: RuleStatus.draft,
    version: "v1",
    audit: { created_at: FIXED_AT, created_by: "openclaw", pack_source: pack("openclaw") },
    match: {
      topics: ["tool.*"],
      velocity: { window: "10s", key: "session_id", threshold: 20 },
    },
    decide: { decision: "throttle", reason: "tool call rate exceeded 20/10s" },
    description: "OpenClaw pack — caps per-session tool call rate.",
  },
  {
    id: "claude-code.edge.deny-destructive",
    name: "Deny destructive shell on edge",
    type: RuleType.edge,
    scope: { kind: RuleScopeKind.global },
    status: RuleStatus.published,
    version: "v1",
    audit: {
      created_at: FIXED_AT,
      created_by: "alice",
      // Edge rule authored interactively via Dashboard 3E; no pack_source.
    },
    match: {
      topics: ["edge.agent_action"],
      capabilities: ["exec.shell"],
      risk_tags: ["destructive"],
    },
    decide: { decision: "deny", reason: "destructive shell denied at edge" },
    description: "Authored via Dashboard 3E — denies destructive shell at the edge.",
  },
];
