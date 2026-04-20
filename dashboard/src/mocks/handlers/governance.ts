import type {
  GovernanceDecision,
  GovernanceDecisionsResponse,
  GovernanceVerdict,
} from "@/api/types";

interface GovernanceMockQuery {
  jobId?: string;
  runId?: string;
  filters?: {
    verdict?: GovernanceVerdict;
    ruleId?: string;
    agentId?: string;
    since?: string;
    until?: string;
  };
  limit?: number;
}

const FIXTURE_DECISIONS: GovernanceDecision[] = [
  {
    jobId: "job-preview",
    topic: "jobs.review",
    matchedRule: "rule-allow",
    ruleName: "Allow trusted request",
    verdict: "allow",
    reason: "Trusted actor within approved scope.",
    constraints: {
      maxInvocations: 5,
      allowedDomains: ["cordum.io"],
    },
    agentId: "agent-green",
    policyVersion: "2026-04-20",
    timestamp: "2026-04-20T10:00:00.000Z",
  },
  {
    jobId: "job-preview",
    topic: "jobs.review",
    matchedRule: "rule-constrain",
    ruleName: "Allow with output guardrails",
    verdict: "constrain",
    reason: "Execution is allowed, but redaction and reviewer constraints stay active.",
    constraints: {
      maskedFields: ["token", "email"],
      requireReviewer: "security-admin",
    },
    agentId: "agent-amber",
    policyVersion: "2026-04-20",
    timestamp: "2026-04-20T10:01:00.000Z",
  },
  {
    jobId: "job-preview",
    runId: "run-preview",
    stepId: "approve-budget",
    topic: "workflow.review",
    matchedRule: "rule-approval",
    ruleName: "Escalate sensitive action",
    verdict: "require_approval",
    reason: "A human reviewer must approve this step before execution continues.",
    constraints: {
      requireReviewer: "finance-approver",
      rateLimit: { requests: 2, windowSeconds: 60 },
    },
    approvalStatus: "pending",
    agentId: "agent-violet",
    policyVersion: "2026-04-20",
    timestamp: "2026-04-20T10:02:00.000Z",
  },
  {
    jobId: "job-preview",
    topic: "jobs.review",
    matchedRule: "rule-deny",
    ruleName: "Block privileged mutation",
    verdict: "deny",
    reason: "Requested action exceeded the tenant's policy boundary.",
    constraints: {
      allowedDomains: ["api.cordum.io"],
      maxInvocations: 1,
    },
    agentId: "agent-purple",
    policyVersion: "2026-04-20",
    timestamp: "2026-04-20T10:03:00.000Z",
  },
  {
    jobId: "job-preview",
    runId: "run-preview",
    stepId: "burst-protection",
    topic: "workflow.review",
    matchedRule: "rule-throttle",
    ruleName: "Rate limit burst traffic",
    verdict: "throttle",
    reason: "Too many policy evaluations in a short interval.",
    constraints: {
      rateLimit: { requests: 10, windowSeconds: 60, burst: 3 },
    },
    agentId: "agent-muted",
    policyVersion: "2026-04-20",
    timestamp: "2026-04-20T10:04:00.000Z",
  },
];

function materializeFixtures(query: GovernanceMockQuery): GovernanceDecision[] {
  return FIXTURE_DECISIONS.map((decision) => ({
    ...decision,
    jobId: query.jobId ?? decision.jobId,
    runId: query.runId ?? decision.runId,
  }));
}

export function getGovernanceMockResponse(
  query: GovernanceMockQuery,
  cursor?: string,
): GovernanceDecisionsResponse {
  const offset = Math.max(0, Number.parseInt(cursor ?? "0", 10) || 0);
  const limit = Math.max(1, query.limit ?? 50);
  const filtered = materializeFixtures(query)
    .filter((decision) =>
      query.filters?.verdict ? decision.verdict === query.filters.verdict : true,
    )
    .filter((decision) =>
      query.filters?.ruleId ? decision.matchedRule === query.filters.ruleId : true,
    )
    .filter((decision) =>
      query.filters?.agentId ? decision.agentId === query.filters.agentId : true,
    )
    .filter((decision) =>
      query.filters?.since
        ? Date.parse(decision.timestamp) >= Date.parse(query.filters.since)
        : true,
    )
    .filter((decision) =>
      query.filters?.until
        ? Date.parse(decision.timestamp) <= Date.parse(query.filters.until)
        : true,
    );

  const items = filtered.slice(offset, offset + limit);
  const nextCursor =
    offset + limit < filtered.length ? String(offset + limit) : undefined;

  return {
    items,
    nextCursor,
  };
}
