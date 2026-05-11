import { EdgeMode } from "@/api/generated/model/edgeMode";

const EDGE_MODE_LABELS: Record<EdgeMode, string> = {
  [EdgeMode.observe]: "Observe",
  [EdgeMode.enforce]: "Enforce",
  [EdgeMode["enterprise-strict"]]: "Enterprise strict",
};

export function edgeModeLabel(m: EdgeMode): string {
  return EDGE_MODE_LABELS[m];
}
