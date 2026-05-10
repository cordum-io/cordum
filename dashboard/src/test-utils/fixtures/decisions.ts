import type { Decision } from "@/api/generated/model/decision";
import { DecisionSource } from "@/api/generated/model/decisionSource";
import { DecisionType } from "@/api/generated/model/decisionType";

const NOW = new Date("2026-05-10T12:00:00Z");
const HOUR = 60 * 60 * 1000;

function isoMinus(offsetMs: number): string {
  return new Date(NOW.getTime() - offsetMs).toISOString();
}

/**
 * Twelve-row sample exercising every Decision field used by Dashboard 8 and
 * Dashboard 9: 8 job-source rows + 4 edge-source rows; all 7 DecisionTypes
 * represented; trace[] populated with 1-3 TraceSteps; bundle_id +
 * bundle_version on most rows; audit_hash on each. Two rows pin the time
 * range to "now" + "now-1h" so the live-vs-historical filter on D8/D9 has
 * deterministic data without invoking Date.now() in tests.
 */
export const fixturePolicyDecisions: Decision[] = [
  {
    source: DecisionSource.job,
    rule_id: "rule.input.secret-scan",
    bundle_id: "bundle.acme.input",
    bundle_version: "v3",
    type: DecisionType.deny,
    trace: [
      {
        rule_id: "rule.input.secret-scan",
        bundle_id: "bundle.acme.input",
        decision_type: DecisionType.deny,
        reason: "matched aws-access-key pattern",
        timestamp: isoMinus(0),
      },
    ],
    input_ref: "blob://acme/in/01HQ",
    output_ref: "",
    audit_hash: "sha256:0001",
    timestamp: isoMinus(0),
  },
  {
    source: DecisionSource.job,
    rule_id: "rule.input.pii-redact",
    bundle_id: "bundle.acme.input",
    bundle_version: "v3",
    type: DecisionType.redact,
    trace: [
      {
        rule_id: "rule.input.pii-redact",
        bundle_id: "bundle.acme.input",
        decision_type: DecisionType.redact,
        reason: "ssn detected, redacted",
        timestamp: isoMinus(5 * 60 * 1000),
      },
    ],
    input_ref: "blob://acme/in/01HR",
    output_ref: "",
    audit_hash: "sha256:0002",
    timestamp: isoMinus(5 * 60 * 1000),
  },
  {
    source: DecisionSource.job,
    rule_id: "rule.velocity.rate-limit",
    bundle_id: "bundle.acme.velocity",
    bundle_version: "v2",
    type: DecisionType.throttle,
    trace: [
      {
        rule_id: "rule.velocity.rate-limit",
        bundle_id: "bundle.acme.velocity",
        decision_type: DecisionType.throttle,
        reason: "tenant exceeded 60 req/min",
        timestamp: isoMinus(10 * 60 * 1000),
      },
    ],
    audit_hash: "sha256:0003",
    timestamp: isoMinus(10 * 60 * 1000),
  },
  {
    source: DecisionSource.job,
    rule_id: "rule.output.quarantine-classifier",
    bundle_id: "bundle.acme.output",
    bundle_version: "v4",
    type: DecisionType.quarantine,
    trace: [
      {
        rule_id: "rule.output.quarantine-classifier",
        bundle_id: "bundle.acme.output",
        decision_type: DecisionType.quarantine,
        reason: "model output flagged 'jailbreak'",
        timestamp: isoMinus(15 * 60 * 1000),
      },
    ],
    output_ref: "blob://acme/out/01HS",
    audit_hash: "sha256:0004",
    timestamp: isoMinus(15 * 60 * 1000),
  },
  {
    source: DecisionSource.job,
    rule_id: "rule.input.approval-gate",
    bundle_id: "bundle.acme.input",
    bundle_version: "v3",
    type: DecisionType.require_human,
    trace: [
      {
        rule_id: "rule.input.approval-gate",
        bundle_id: "bundle.acme.input",
        decision_type: DecisionType.require_human,
        reason: "high-cost capability invoked",
        timestamp: isoMinus(30 * 60 * 1000),
      },
    ],
    audit_hash: "sha256:0005",
    timestamp: isoMinus(30 * 60 * 1000),
  },
  {
    source: DecisionSource.job,
    rule_id: "rule.input.allow-default",
    bundle_id: "bundle.acme.input",
    bundle_version: "v3",
    type: DecisionType.allow,
    trace: [
      {
        rule_id: "rule.input.allow-default",
        decision_type: DecisionType.allow,
        reason: "no matching deny",
        timestamp: isoMinus(45 * 60 * 1000),
      },
    ],
    audit_hash: "sha256:0006",
    timestamp: isoMinus(45 * 60 * 1000),
  },
  {
    source: DecisionSource.job,
    rule_id: "rule.velocity.budget-cap",
    bundle_id: "bundle.acme.velocity",
    bundle_version: "v2",
    type: DecisionType.allow_with_constraints,
    trace: [
      {
        rule_id: "rule.velocity.budget-cap",
        bundle_id: "bundle.acme.velocity",
        decision_type: DecisionType.allow_with_constraints,
        reason: "applied $0.50 cap",
        timestamp: isoMinus(50 * 60 * 1000),
        constraints: { budget_cents: 50, sandbox: "isolated" },
      },
    ],
    audit_hash: "sha256:0007",
    timestamp: isoMinus(50 * 60 * 1000),
  },
  {
    source: DecisionSource.job,
    rule_id: "rule.input.deny-bash",
    bundle_id: "bundle.acme.input",
    bundle_version: "v3",
    type: DecisionType.deny,
    trace: [
      {
        rule_id: "rule.input.deny-bash",
        decision_type: DecisionType.deny,
        reason: "tool 'bash.exec' blocked at tier global",
        timestamp: isoMinus(HOUR - 5 * 60 * 1000),
      },
    ],
    audit_hash: "sha256:0008",
    timestamp: isoMinus(HOUR - 5 * 60 * 1000),
  },
  {
    source: DecisionSource.edge,
    rule_id: "edge.tool.shell-block",
    bundle_id: "bundle.acme.edge",
    bundle_version: "v1",
    type: DecisionType.deny,
    trace: [
      {
        rule_id: "edge.tool.shell-block",
        bundle_id: "bundle.acme.edge",
        decision_type: DecisionType.deny,
        reason: "shell.exec denied at edge",
        timestamp: isoMinus(2 * 60 * 1000),
      },
    ],
    input_ref: "blob://edge/01HT",
    audit_hash: "sha256:0009",
    timestamp: isoMinus(2 * 60 * 1000),
  },
  {
    source: DecisionSource.edge,
    rule_id: "edge.file.write-restrict",
    bundle_id: "bundle.acme.edge",
    bundle_version: "v1",
    type: DecisionType.allow_with_constraints,
    trace: [
      {
        rule_id: "edge.file.write-restrict",
        bundle_id: "bundle.acme.edge",
        decision_type: DecisionType.allow_with_constraints,
        reason: "file.write scoped to repo",
        timestamp: isoMinus(20 * 60 * 1000),
        constraints: { allowed_paths: ["./repo"] },
      },
    ],
    audit_hash: "sha256:0010",
    timestamp: isoMinus(20 * 60 * 1000),
  },
  {
    source: DecisionSource.edge,
    rule_id: "edge.prompt.classifier",
    bundle_id: "bundle.acme.edge",
    bundle_version: "v1",
    type: DecisionType.require_human,
    trace: [
      {
        rule_id: "edge.prompt.classifier",
        bundle_id: "bundle.acme.edge",
        decision_type: DecisionType.require_human,
        reason: "prompt scored 0.87 risk",
        timestamp: isoMinus(40 * 60 * 1000),
      },
    ],
    audit_hash: "sha256:0011",
    timestamp: isoMinus(40 * 60 * 1000),
  },
  {
    source: DecisionSource.edge,
    rule_id: "edge.output.sanitize-secrets",
    bundle_id: "bundle.acme.edge",
    bundle_version: "v1",
    type: DecisionType.redact,
    trace: [
      {
        rule_id: "edge.output.sanitize-secrets",
        bundle_id: "bundle.acme.edge",
        decision_type: DecisionType.redact,
        reason: "secret detected in tool output",
        timestamp: isoMinus(HOUR),
      },
    ],
    output_ref: "blob://edge/01HV",
    audit_hash: "sha256:0012",
    timestamp: isoMinus(HOUR),
  },
];
