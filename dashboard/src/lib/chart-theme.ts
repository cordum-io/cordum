/**
 * Control Surface chart defaults.
 * Shared Recharts configuration, semantic palette, and helpers.
 */

// ---------------------------------------------------------------------------
// Semantic palette — safety decision colors
// ---------------------------------------------------------------------------

export const chartColors = {
  allow: "#1f7a57",
  deny: "#7c3aed",
  require_approval: "#c58a1c",
  allow_with_constraints: "#0f7f7a",
  throttle: "#d4833a",
  cordum: "#0f7f7a",
  muted: "#5a6a70",
} as const;

export type ChartColorKey = keyof typeof chartColors;

/** Resolve a semantic key or pass through a raw hex color. */
export function resolveChartColor(key: string): string {
  return (chartColors as Record<string, string>)[key] ?? key;
}

// ---------------------------------------------------------------------------
// Gradient helpers (for AreaChart fills)
// ---------------------------------------------------------------------------

export function gradientId(key: string): string {
  return `grad-${key}`;
}

export function gradientFill(key: string): string {
  return `url(#${gradientId(key)})`;
}

// ---------------------------------------------------------------------------
// Axis / grid shared defaults
// ---------------------------------------------------------------------------

export const axisTickStyle = {
  fontSize: 10,
  fontFamily: "'JetBrains Mono', monospace",
  fill: "#5a6a70",
} as const;

export const gridProps = {
  strokeDasharray: "3 3",
  stroke: "rgba(255,255,255,0.04)",
} as const;

// ---------------------------------------------------------------------------
// Tooltip surface
// ---------------------------------------------------------------------------

export const tooltipStyle = {
  background: "var(--surface-2, #1f2a2e)",
  border: "1px solid var(--border-color, #1f2a2e)",
  borderRadius: 8,
  padding: 12,
  boxShadow: "0 8px 32px rgba(0,0,0,0.3)",
} as const;

// ---------------------------------------------------------------------------
// Bar chart shared defaults
// ---------------------------------------------------------------------------

export const barDefaults = {
  radius: [3, 3, 0, 0] as [number, number, number, number],
  maxBarSize: 32,
} as const;

// ---------------------------------------------------------------------------
// Decision label mapping
// ---------------------------------------------------------------------------

export const decisionLabels: Record<string, string> = {
  allow: "Allow",
  deny: "Deny",
  require_approval: "Approval",
  allow_with_constraints: "Constrained",
  throttle: "Throttle",
};

export function getDecisionLabel(decision: string): string {
  return decisionLabels[decision] ?? decision;
}
