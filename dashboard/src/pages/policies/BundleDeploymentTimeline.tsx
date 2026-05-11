import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Calendar } from "lucide-react";
import type { BundleDeployment } from "@/hooks/useBundle";
import { formatRelativeTime } from "@/lib/utils";
import {
  computeTimelineSegments,
  uniqueVersions,
  versionColorIndex,
  type TimelineRange,
  type TimelineSegment,
} from "@/lib/policy-studio/timeline-segments";

/**
 * Bundle deployment timeline (D6) — Gantt-style horizontal chart that
 * answers "what was active at time T on scope X?" by drawing one row
 * per scope and one colour-coded segment per active deployment.
 *
 * Render strategy: SVG-direct (Recharts has no Gantt primitive; per
 * planningNotes, an external Gantt lib violates the no-new-deps rail).
 * Per-version colours are rotated through 5 existing CSS-variable
 * tokens — no hex literals, no new colour palette.
 *
 * Path-A scope (per the predecessor worker-c1cf step-1 finding): the
 * tooltip surfaces version + deployed_at + scope only. Author + audit
 * hash are deferred until Backend 2.5 extends the BundleDeployment
 * shape with deployed_by/audit_hash; the segment-action enum is
 * deferred similarly. The component contract is shaped so the eventual
 * Backend 2.5 swap is one-line additive.
 */

const RANGE_PRESETS = [
  { id: "1d" as const, label: "1d", days: 1 },
  { id: "7d" as const, label: "7d", days: 7 },
  { id: "30d" as const, label: "30d", days: 30 },
];

type PresetId = (typeof RANGE_PRESETS)[number]["id"];

const VERSION_COLOR_TOKENS = [
  "var(--color-cordum, #1f6feb)",
  "var(--color-success, #1f7a57)",
  "var(--color-warning, #c58a1c)",
  "var(--color-info, #0f7f7a)",
  "var(--color-accent, #8b5cf6)",
];

const SVG_HEIGHT_PER_ROW = 28;
const SVG_ROW_PADDING = 8;
const SVG_PADDING_LEFT = 140;
const SVG_PADDING_RIGHT = 16;
const SVG_PADDING_TOP = 24;
const SVG_PADDING_BOTTOM = 24;
const MOBILE_HIDE_BREAKPOINT_PX = 720;

interface BundleDeploymentTimelineProps {
  bundleId: string;
  deployments: ReadonlyArray<BundleDeployment>;
  /**
   * Override "now" for deterministic rendering in tests. Defaults to
   * `Date.now()`. Passing this lets the open-ended-segment cap line up
   * with a fixed test fixture so screenshot/byte assertions stabilise.
   */
  nowMs?: number;
}

/** Public entry — handles range state + mobile gate; defers SVG to BundleDeploymentTimelineSvg. */
export function BundleDeploymentTimeline({
  bundleId,
  deployments,
  nowMs,
}: BundleDeploymentTimelineProps) {
  const [presetId, setPresetId] = useState<PresetId>("30d");
  const range: TimelineRange = useMemo(() => {
    const now = typeof nowMs === "number" ? nowMs : Date.now();
    const preset = RANGE_PRESETS.find((p) => p.id === presetId) ?? RANGE_PRESETS[2];
    return { fromMs: now - preset.days * 86_400_000, toMs: now };
  }, [presetId, nowMs]);

  const segments = useMemo(
    () => computeTimelineSegments(deployments, range),
    [deployments, range],
  );

  if (segments.length === 0) {
    return (
      <BundleDeploymentTimelineFrame
        presetId={presetId}
        onPresetChange={setPresetId}
      >
        <BundleDeploymentTimelineEmpty />
      </BundleDeploymentTimelineFrame>
    );
  }

  return (
    <BundleDeploymentTimelineFrame
      presetId={presetId}
      onPresetChange={setPresetId}
    >
      {/* Mobile fallback: Gantt is unreadable below 720px. The matrix
          below it on BundleDeploymentsTab is the mobile-friendly view. */}
      <p
        className="rounded-2xl border border-border bg-surface-2 p-3 text-xs text-muted-foreground sm:hidden"
        data-testid="bundle-timeline-mobile-fallback"
      >
        Open this page on a wider screen ({MOBILE_HIDE_BREAKPOINT_PX}px+) to view the
        deployment timeline. The scope × version matrix below remains usable on
        narrow viewports.
      </p>
      <div className="hidden sm:block">
        <BundleDeploymentTimelineSvg
          bundleId={bundleId}
          segments={segments}
          range={range}
        />
      </div>
    </BundleDeploymentTimelineFrame>
  );
}

function BundleDeploymentTimelineFrame({
  presetId,
  onPresetChange,
  children,
}: {
  presetId: PresetId;
  onPresetChange: (id: PresetId) => void;
  children: React.ReactNode;
}) {
  return (
    <section
      aria-labelledby="bundle-timeline-heading"
      className="space-y-3 rounded-2xl border border-border bg-surface-1 p-4"
    >
      <header className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Calendar aria-hidden className="h-4 w-4 text-cordum" />
          <h3
            id="bundle-timeline-heading"
            className="text-sm font-semibold text-foreground"
          >
            Deployment timeline
          </h3>
        </div>
        <div
          role="radiogroup"
          aria-label="Timeline range"
          className="flex items-center gap-1 rounded-xl border border-border bg-surface-2 p-1 text-xs"
        >
          {RANGE_PRESETS.map((preset) => {
            const active = preset.id === presetId;
            return (
              <button
                key={preset.id}
                type="button"
                role="radio"
                aria-checked={active}
                onClick={() => onPresetChange(preset.id)}
                data-preset-id={preset.id}
                className={
                  active
                    ? "rounded-lg bg-cordum px-2 py-1 font-medium text-white"
                    : "rounded-lg px-2 py-1 text-muted-foreground hover:text-foreground"
                }
              >
                {preset.label}
              </button>
            );
          })}
        </div>
      </header>
      {children}
    </section>
  );
}

function BundleDeploymentTimelineEmpty() {
  return (
    <p
      className="rounded-2xl border border-border bg-surface-2 p-6 text-center text-xs text-muted-foreground"
      data-testid="bundle-timeline-empty"
    >
      No deployments to display in this time range.
    </p>
  );
}

interface BundleDeploymentTimelineSvgProps {
  bundleId: string;
  segments: ReadonlyArray<TimelineSegment>;
  range: TimelineRange;
}

function BundleDeploymentTimelineSvg({
  bundleId,
  segments,
  range,
}: BundleDeploymentTimelineSvgProps) {
  const [searchParams] = useSearchParams();
  void searchParams;
  const versionOrder = useMemo(() => uniqueVersions(segments), [segments]);
  const scopeKeys = useMemo(() => {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const s of segments) {
      if (!seen.has(s.scopeKey)) {
        seen.add(s.scopeKey);
        out.push(s.scopeKey);
      }
    }
    return out.sort((a, b) => a.localeCompare(b));
  }, [segments]);

  const scopeLabels = useMemo(() => {
    const map = new Map<string, string>();
    for (const s of segments) {
      if (!map.has(s.scopeKey)) map.set(s.scopeKey, s.scopeLabel);
    }
    return map;
  }, [segments]);

  const widthPx = 760;
  const heightPx =
    SVG_PADDING_TOP +
    SVG_PADDING_BOTTOM +
    scopeKeys.length * (SVG_HEIGHT_PER_ROW + SVG_ROW_PADDING);
  const trackWidth = widthPx - SVG_PADDING_LEFT - SVG_PADDING_RIGHT;
  const totalMs = Math.max(1, range.toMs - range.fromMs);

  function xFor(ms: number): number {
    const clamped = Math.min(Math.max(ms, range.fromMs), range.toMs);
    return SVG_PADDING_LEFT + ((clamped - range.fromMs) / totalMs) * trackWidth;
  }

  return (
    <svg
      role="img"
      aria-label={`Deployment timeline for bundle ${bundleId}`}
      viewBox={`0 0 ${widthPx} ${heightPx}`}
      className="w-full"
      data-testid="bundle-timeline-svg"
    >
      {/* Now-line — drawn at range.toMs for context. */}
      <line
        x1={xFor(range.toMs)}
        x2={xFor(range.toMs)}
        y1={SVG_PADDING_TOP - 6}
        y2={heightPx - SVG_PADDING_BOTTOM + 6}
        stroke="var(--color-border, #d4d4d4)"
        strokeDasharray="2 3"
      />
      {/* Per-scope rows. */}
      {scopeKeys.map((scopeKey, rowIdx) => {
        const rowY =
          SVG_PADDING_TOP + rowIdx * (SVG_HEIGHT_PER_ROW + SVG_ROW_PADDING);
        const rowSegments = segments.filter((s) => s.scopeKey === scopeKey);
        return (
          <g key={scopeKey} data-row-scope={scopeKey}>
            <text
              x={SVG_PADDING_LEFT - 8}
              y={rowY + SVG_HEIGHT_PER_ROW / 2 + 4}
              textAnchor="end"
              fontSize={11}
              className="fill-muted-foreground font-mono"
            >
              {scopeLabels.get(scopeKey) ?? scopeKey}
            </text>
            {rowSegments.map((seg) => {
              const startX = xFor(seg.startMs);
              const endX = xFor(seg.endMs ?? range.toMs);
              const w = Math.max(2, endX - startX);
              const idx = versionColorIndex(seg.version, versionOrder);
              const fill =
                VERSION_COLOR_TOKENS[idx % VERSION_COLOR_TOKENS.length];
              const linkHref = `/policies/bundles/${encodeURIComponent(bundleId)}?tab=versions&v=${encodeURIComponent(seg.version)}`;
              return (
                <Link
                  key={`${seg.scopeKey}-${seg.startMs}`}
                  to={linkHref}
                  data-segment-version={seg.version}
                  data-segment-scope={seg.scopeKey}
                  data-row-action
                >
                  <rect
                    x={startX}
                    y={rowY}
                    width={w}
                    height={SVG_HEIGHT_PER_ROW}
                    rx={4}
                    fill={fill}
                    fillOpacity={0.85}
                  />
                  <title>
                    {`Version ${seg.version} on ${seg.scopeLabel}\nDeployed ${formatRelativeTime(seg.source.deployed_at)} (${seg.source.deployed_at})`}
                  </title>
                </Link>
              );
            })}
          </g>
        );
      })}
    </svg>
  );
}

export default BundleDeploymentTimeline;
