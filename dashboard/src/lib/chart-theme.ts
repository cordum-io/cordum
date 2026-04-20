/**
 * Chart Theme — Control Surface defaults for Recharts
 *
 * Provides shared chart configuration (colors, grid, axis, tooltip, bar props)
 * aligned with the Control Surface design language tokens. Use these helpers
 * instead of inline hex values in chart components.
 */

// ---------------------------------------------------------------------------
// Semantic chart palette
// ---------------------------------------------------------------------------

/** Semantic color map for decision/status visualizations */
export const SEMANTIC_COLORS = {
  allow: "var(--success)",
  deny: "var(--danger)",
  require_approval: "var(--warning)",
  throttle: "var(--muted)",
  pending: "var(--warning)",
  running: "var(--accent)",
  succeeded: "var(--success)",
  failed: "var(--danger)",
  timeout: "var(--danger)",
} as const;

/** Get a semantic color by key, with a muted fallback */
export function semanticColor(key: string): string {
  return (SEMANTIC_COLORS as Record<string, string>)[key] ?? "var(--muted)";
}

// ---------------------------------------------------------------------------
// Shared axis/grid configuration
// ---------------------------------------------------------------------------

/** Default CartesianGrid props (low-opacity dashed grid) */
export const gridProps = {
  strokeDasharray: "3 3",
  stroke: "var(--border)",
  strokeOpacity: 0.6,
} as const;

/** Default XAxis/YAxis tick style (mono, small, muted) */
export const axisTickStyle = {
  fontSize: 10,
  fontFamily: '"IBM Plex Mono", monospace',
  fill: "var(--muted)",
} as const;

/** Default axis line props (hidden for clean look) */
export const axisLineProps = {
  axisLine: false,
  tickLine: false,
} as const;

// ---------------------------------------------------------------------------
// Tooltip
// ---------------------------------------------------------------------------

/** Default tooltip content style (surface card background) */
export const tooltipStyle = {
  backgroundColor: "var(--surface)",
  border: "1px solid var(--border)",
  borderRadius: 12,
  padding: "8px 12px",
  fontSize: 12,
  boxShadow: "0 8px 24px var(--shadow)",
} as const;

// ---------------------------------------------------------------------------
// Bar/Area defaults
// ---------------------------------------------------------------------------

/** Default bar chart props (rounded corners, compact radius) */
export const barDefaults = {
  radius: [4, 4, 0, 0] as [number, number, number, number],
  maxBarSize: 40,
} as const;

/** Default area chart props */
export const areaDefaults = {
  type: "monotone" as const,
  strokeWidth: 2,
  fillOpacity: 0.15,
} as const;

// ---------------------------------------------------------------------------
// Chart motion
// ---------------------------------------------------------------------------

/** Animation props that respect prefers-reduced-motion */
export const chartMotionProps = {
  isAnimationActive: typeof window !== "undefined" && typeof window.matchMedia === "function"
    ? !window.matchMedia("(prefers-reduced-motion: reduce)").matches
    : true,
  animationDuration: 400,
  animationEasing: "ease-out" as const,
} as const;
