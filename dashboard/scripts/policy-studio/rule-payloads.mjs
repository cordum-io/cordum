/*
 * Source-of-truth for the per-RuleType `match`/`decide` payload schemas
 * used by Policy Studio's Rules editor.
 *
 * The unified Rule envelope (id, name, type, scope, status, version, audit,
 * description) is fixed by the OpenAPI spec at
 * `cordum/docs/api/openapi/cordum-api.yaml` (Rule schema). The envelope's
 * `match` and `decide` fields are intentionally `additionalProperties: true`
 * because the per-type payload is opaque to the unified transport. The
 * authoring surface in this dashboard, however, knows the per-type shape:
 * Phase 3B exposes structured fields for each of the four rule types, and
 * Monaco's YAML diagnostics validate against the same shape.
 *
 * `scripts/generate-schemas.mjs` consumes this module: it reads the unified
 * envelope from the OpenAPI YAML and combines it with the payload fragment
 * here for each rule type, emitting a complete JSON Schema per type into
 * `src/lib/policy-studio/schemas/generated/{type}.json`.
 *
 * Hand-edit this file to evolve the authoring surface; regenerate with
 * `pnpm run generate-schemas` and CI's `pnpm run check-schemas` will fail
 * the PR until the committed JSON Schemas reflect the change.
 */

const decisionTypes = [
  "allow",
  "deny",
  "require_human",
  "throttle",
  "allow_with_constraints",
  "quarantine",
  "redact",
];

const stringArray = {
  type: "array",
  items: { type: "string", minLength: 1 },
  uniqueItems: true,
};

export const rulePayloads = {
  input: {
    title: "Input rule payload",
    description:
      "Pre-execution job-side input policy. Match selects which content the rule applies to; decide chooses the unified DecisionType. Aligns with safetykernel input scanners.",
    match: {
      type: "object",
      additionalProperties: false,
      properties: {
        topics: { ...stringArray, description: "Topics this rule applies to (e.g. `email.draft`)." },
        tools: { ...stringArray, description: "Tool names triggering this rule (e.g. `Bash`, `Edit`)." },
        risk_tags: { ...stringArray, description: "Required risk tags on the job context for the rule to fire." },
        content_pattern: {
          type: "string",
          maxLength: 1000,
          description: "Optional regex applied to the input content; rule fires only when content matches.",
        },
      },
    },
    decide: {
      type: "object",
      additionalProperties: false,
      required: ["type"],
      properties: {
        type: { enum: decisionTypes, description: "Unified decision outcome." },
        reason: { type: "string", maxLength: 500, description: "Human-readable rationale logged with the decision." },
      },
    },
  },

  output: {
    title: "Output rule payload",
    description:
      "Post-execution output safety scan. Match scopes by topic/tool/risk and finding types; decide narrows the unified DecisionType to outcomes meaningful for output (allow/deny/redact/quarantine/require_human).",
    match: {
      type: "object",
      additionalProperties: false,
      properties: {
        topics: { ...stringArray, description: "Topics this rule applies to." },
        tools: { ...stringArray, description: "Tool names whose output is scanned by this rule." },
        risk_tags: { ...stringArray, description: "Required risk tags on the job context." },
        finding_types: {
          type: "array",
          items: { enum: ["secret_leak", "pii", "injection"] },
          uniqueItems: true,
          description: "Output scanner finding types this rule reacts to.",
        },
      },
    },
    decide: {
      type: "object",
      additionalProperties: false,
      required: ["type"],
      properties: {
        type: {
          enum: ["allow", "deny", "redact", "quarantine", "require_human"],
          description: "Output-meaningful decision outcomes.",
        },
        reason: { type: "string", maxLength: 500, description: "Human-readable rationale." },
        redact_strategy: {
          type: "string",
          maxLength: 200,
          description: "Optional strategy hint passed to the output redactor (e.g. `mask`, `tokenize`).",
        },
      },
    },
  },

  velocity: {
    title: "Velocity rule payload",
    description:
      "Rate-limiting rule. Match scopes by tenant/topic/risk; decide is always `throttle` with at least one of the per-window thresholds set.",
    match: {
      type: "object",
      additionalProperties: false,
      properties: {
        tenants: { ...stringArray, description: "Tenant ids this rule applies to (empty = all)." },
        topics: { ...stringArray, description: "Topics this rule applies to (empty = all)." },
        risk_tags: { ...stringArray, description: "Required risk tags on the job context." },
      },
    },
    decide: {
      type: "object",
      additionalProperties: false,
      required: ["type"],
      properties: {
        type: { const: "throttle", description: "Velocity rules always throttle." },
        max_per_minute: { type: "integer", minimum: 1, description: "Per-minute cap; rule fires when exceeded." },
        max_per_hour: { type: "integer", minimum: 1, description: "Per-hour cap." },
        max_per_day: { type: "integer", minimum: 1, description: "Per-day cap." },
        burst_limit: { type: "integer", minimum: 1, description: "Allowed burst above the per-minute rate." },
        reason: { type: "string", maxLength: 500, description: "Human-readable rationale." },
      },
      anyOf: [
        { required: ["max_per_minute"] },
        { required: ["max_per_hour"] },
        { required: ["max_per_day"] },
      ],
    },
  },

  edge: {
    title: "Edge rule payload",
    description:
      "Edge-side policy for Claude Code command-hook sessions. Match scopes by tool name and optional regex on command/path; decide narrows DecisionType to outcomes meaningful at the edge (allow/deny/require_human/allow_with_constraints).",
    match: {
      type: "object",
      additionalProperties: false,
      properties: {
        tools: { ...stringArray, description: "Edge tool names (e.g. `Bash`, `Edit`, `Read`)." },
        command_pattern: {
          type: "string",
          maxLength: 1000,
          description: "Optional regex applied to the command text. Rule fires only on match.",
        },
        path_pattern: {
          type: "string",
          maxLength: 1000,
          description: "Optional regex applied to the affected path (Edit/Read/Write).",
        },
        risk_tags: { ...stringArray, description: "Required risk tags on the edge context." },
      },
    },
    decide: {
      type: "object",
      additionalProperties: false,
      required: ["type"],
      properties: {
        type: {
          enum: ["allow", "deny", "require_human", "allow_with_constraints"],
          description: "Edge-meaningful decision outcomes.",
        },
        reason: { type: "string", maxLength: 500, description: "Human-readable rationale." },
      },
    },
  },
};

export const ruleTypeOrder = ["input", "output", "velocity", "edge"];
