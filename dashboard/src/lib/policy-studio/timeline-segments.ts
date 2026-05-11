import type { BundleDeployment } from "@/hooks/useBundle";

/**
 * One contiguous span where a single bundle version was active on a
 * given scope. Open-ended at the right edge means "still active as of
 * the time-range end" — the timeline renders that segment running into
 * a "now" cap.
 */
export interface TimelineSegment {
  scopeKey: string;
  scopeLabel: string;
  version: string;
  /** Inclusive start time, ms since epoch. */
  startMs: number;
  /** Exclusive end time, ms since epoch. `null` means "still active". */
  endMs: number | null;
  /** The deployment event that opened this span. */
  source: BundleDeployment;
}

/**
 * Time range the Gantt is rendering. Both fields are required so the
 * caller decides what "now" means (tests inject a fixed nowMs to make
 * the open-ended segment deterministic).
 */
export interface TimelineRange {
  fromMs: number;
  toMs: number;
}

/**
 * Stable scope key for grouping deployments: `<kind>:<value>` when both
 * sides are present, else falls back to the legacy `scope` field, else
 * "global". Matches the Bundles tab's scopeLabel computation so the
 * Gantt rows align with the matrix below it.
 */
export function scopeKeyOf(deployment: BundleDeployment): string {
  const kind = deployment.scope_kind?.trim();
  const value = deployment.scope_value?.trim();
  if (kind && value) return `${kind}:${value}`;
  if (kind) return kind;
  return deployment.scope?.trim() || "global";
}

/**
 * Render-friendly scope label. Identical to `scopeKeyOf` for the
 * usual `<kind>:<value>` shape; left as a separate function so the
 * UI can later humanise certain values without breaking key equality.
 */
export function scopeLabelOf(deployment: BundleDeployment): string {
  return scopeKeyOf(deployment);
}

/**
 * Computes per-scope segments from a flat deployment-event history.
 * Each event's `deployed_at` opens a new segment for that scope; the
 * previous segment (if any) closes at the same instant. The newest
 * event on each scope yields an open-ended segment (endMs=null) which
 * the renderer caps at `range.toMs`.
 *
 * Events with unparseable timestamps are dropped; the segment list
 * never throws on bad input. Events outside `range` are kept iff they
 * straddle the range — a segment that started before `range.fromMs`
 * and is still active gets clamped at `fromMs` by the renderer (NOT
 * here, so the test can still see the original timestamp).
 *
 * The returned segments are sorted by (scopeKey, startMs ascending).
 */
export function computeTimelineSegments(
  deployments: ReadonlyArray<BundleDeployment>,
  range: TimelineRange,
): TimelineSegment[] {
  // Group by scope, parse timestamps, keep only well-formed events.
  const byScope = new Map<string, Array<{ d: BundleDeployment; ms: number }>>();
  for (const d of deployments) {
    const ms = parseTimestamp(d.deployed_at);
    if (ms === null) continue;
    const key = scopeKeyOf(d);
    let list = byScope.get(key);
    if (!list) {
      list = [];
      byScope.set(key, list);
    }
    list.push({ d, ms });
  }

  const out: TimelineSegment[] = [];
  for (const [scopeKey, events] of byScope) {
    // Sort each scope's events by deployed_at ascending; multi-rollback
    // chains rely on this order to compute startTime/endTime correctly.
    events.sort((a, b) => a.ms - b.ms);
    for (let i = 0; i < events.length; i++) {
      const cur = events[i];
      const next = events[i + 1];
      // Drop events with deployed_at past the visible range — they're
      // future-dated metadata or test fixtures that confuse the chart.
      if (cur.ms > range.toMs) continue;
      // Only open-ended segments may be the last in the array. Internal
      // segments inherit `endMs` from the next event's start. Equal
      // timestamps degrade to a 1ms sliver so the renderer still draws
      // it (a visible "this version was rolled back immediately" tag).
      const endMs =
        next === undefined
          ? null
          : Math.max(cur.ms + 1, next.ms);
      out.push({
        scopeKey,
        scopeLabel: scopeLabelOf(cur.d),
        version: cur.d.version,
        startMs: cur.ms,
        endMs,
        source: cur.d,
      });
    }
  }
  out.sort((a, b) =>
    a.scopeKey === b.scopeKey
      ? a.startMs - b.startMs
      : a.scopeKey.localeCompare(b.scopeKey),
  );
  return out;
}

/**
 * Maps a version label to a stable index for color rotation. Reusing
 * the same version across deployments yields the same colour so a
 * rollback to v1 visually equals the original v1 segment.
 */
export function versionColorIndex(
  version: string,
  versionOrder: ReadonlyArray<string>,
): number {
  const idx = versionOrder.indexOf(version);
  return idx >= 0 ? idx : versionOrder.length;
}

/** Returns the unique versions in the order they first appear in segments. */
export function uniqueVersions(
  segments: ReadonlyArray<TimelineSegment>,
): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const s of segments) {
    if (!seen.has(s.version)) {
      seen.add(s.version);
      out.push(s.version);
    }
  }
  return out;
}

function parseTimestamp(raw: string): number | null {
  if (!raw) return null;
  const ms = Date.parse(raw);
  return Number.isFinite(ms) ? ms : null;
}
