export const TAB_IDS = [
  "overview",
  "input-rules",
  "output-rules",
  "velocity",
  "evaluation",
  "bundles",
  "scope",
] as const;

export type PolicyStudioTab = (typeof TAB_IDS)[number];

export function isValidTab(tab: string): tab is PolicyStudioTab {
  return (TAB_IDS as readonly string[]).includes(tab);
}

export const EVALUATION_MODE_IDS = [
  "analytics",
  "replay",
  "simulator",
] as const;

export type PolicyEvaluationMode = (typeof EVALUATION_MODE_IDS)[number];

export function isValidEvaluationMode(
  mode: string,
): mode is PolicyEvaluationMode {
  return (EVALUATION_MODE_IDS as readonly string[]).includes(mode);
}
