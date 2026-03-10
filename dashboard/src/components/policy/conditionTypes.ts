import type { PolicyRule } from "../../api/types";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type ConditionField = "capability" | "riskTag" | "topic" | "agentPool";
export type ConditionOperator = "equals" | "contains" | "in" | "not_in";

export interface Condition {
  id: string;
  field: ConditionField;
  operator: ConditionOperator;
  value: string | string[];
}

export interface ConditionGroup {
  id: string;
  logic: "AND" | "OR";
  conditions: (Condition | ConditionGroup)[];
}

export function isConditionGroup(
  item: Condition | ConditionGroup,
): item is ConditionGroup {
  return "logic" in item && "conditions" in item;
}

// ---------------------------------------------------------------------------
// Factories
// ---------------------------------------------------------------------------

let _counter = 0;
function uid(): string {
  return `cond-${Date.now()}-${++_counter}`;
}

export function createCondition(
  field: ConditionField = "capability",
  operator: ConditionOperator = "in",
  value: string | string[] = [],
): Condition {
  return { id: uid(), field, operator, value };
}

export function createConditionGroup(
  logic: "AND" | "OR" = "AND",
  conditions: (Condition | ConditionGroup)[] = [],
): ConditionGroup {
  return { id: uid(), logic, conditions };
}

// ---------------------------------------------------------------------------
// fromRule — convert a flat PolicyRule into a ConditionGroup for editing
// ---------------------------------------------------------------------------

export function fromRule(rule: PolicyRule): ConditionGroup {
  const items: Condition[] = [];

  const caps =
    (rule.matchCriteria?.capabilities as string[] | undefined) ?? [];
  if (caps.length > 0) {
    items.push(createCondition("capability", "in", [...caps]));
  }

  const tags =
    (rule.matchCriteria?.riskTags as string[] | undefined) ?? [];
  if (tags.length > 0) {
    items.push(createCondition("riskTag", "in", [...tags]));
  }

  const logic: "AND" | "OR" = rule.logic === "OR" ? "OR" : "AND";
  return createConditionGroup(logic, items);
}

// ---------------------------------------------------------------------------
// toMatchCriteria — convert ConditionGroup tree into API-compatible object
// ---------------------------------------------------------------------------

export function toMatchCriteria(group: ConditionGroup): {
  capabilities: string[];
  riskTags: string[];
  logic: "AND" | "OR";
  groups?: ConditionGroup[];
} {
  const capabilities: string[] = [];
  const riskTags: string[] = [];
  let hasNested = false;

  for (const item of group.conditions) {
    if (isConditionGroup(item)) {
      hasNested = true;
    } else {
      const vals = Array.isArray(item.value)
        ? item.value
        : item.value
          ? [item.value]
          : [];
      if (item.field === "capability") {
        capabilities.push(...vals);
      } else if (item.field === "riskTag") {
        riskTags.push(...vals);
      }
    }
  }

  const result: {
    capabilities: string[];
    riskTags: string[];
    logic: "AND" | "OR";
    groups?: ConditionGroup[];
  } = { capabilities, riskTags, logic: group.logic };

  if (hasNested) {
    result.groups = group.conditions.filter(isConditionGroup);
  }

  return result;
}

// ---------------------------------------------------------------------------
// toYaml — convert ConditionGroup into a human-readable YAML string
// ---------------------------------------------------------------------------

function indented(depth: number): string {
  return "  ".repeat(depth);
}

function conditionToYaml(cond: Condition, depth: number): string {
  const val = Array.isArray(cond.value)
    ? `[${cond.value.join(", ")}]`
    : cond.value;
  return `${indented(depth)}- field: ${cond.field}\n${indented(depth)}  operator: ${cond.operator}\n${indented(depth)}  value: ${val}`;
}

function groupToYaml(group: ConditionGroup, depth: number): string {
  const lines: string[] = [];
  lines.push(`${indented(depth)}logic: ${group.logic}`);
  lines.push(`${indented(depth)}conditions:`);
  for (const item of group.conditions) {
    if (isConditionGroup(item)) {
      lines.push(`${indented(depth + 1)}- group:`);
      lines.push(groupToYaml(item, depth + 2));
    } else {
      lines.push(conditionToYaml(item, depth + 1));
    }
  }
  return lines.join("\n");
}

export function toYaml(
  group: ConditionGroup,
  decisionType: string,
  reason: string,
): string {
  const lines: string[] = [];
  lines.push("match:");
  lines.push(groupToYaml(group, 1));
  lines.push(`decision: ${decisionType}`);
  if (reason) {
    lines.push(`reason: "${reason}"`);
  }
  return lines.join("\n");
}
