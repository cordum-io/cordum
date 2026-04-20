import { lazy } from "react";
import type { LucideIcon } from "lucide-react";

// Lazy-loaded tab content — each wraps its page with hideHeader=true
export const LazyInputRulesTab = lazy(() => import("./InputRulesTab"));
export const LazyOutputRulesTab = lazy(() => import("./OutputRulesTab"));
export const LazySimulatorTab = lazy(() => import("./SimulatorTab"));
export const LazyBundlesTab = lazy(() => import("./BundlesTab"));

export interface TabDefinition {
  id: string;
  label: string;
  count?: number;
}

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
