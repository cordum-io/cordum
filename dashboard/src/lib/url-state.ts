/**
 * Canonical URL search-param parsers for hero-page filter UI.
 *
 * Built on nuqs ^2 primitives. Hero pages should import their filter parsers
 * from here so that recurring shapes (search input, 1-based page number,
 * time-range presets, enum filters) stay consistent across surfaces.
 *
 * For one-off shapes, hero pages may import nuqs primitives directly.
 *
 * Coexistence note: nuqs and react-router's `useSearchParams` operate on the
 * same URL search string and CAN coexist, but a given query key MUST be
 * owned by exactly one of them — concurrent writes from both will race and
 * the loser's update will be silently overwritten on the next render. When
 * migrating a page to nuqs, audit every existing `setSearchParams` call site
 * and remove keys that nuqs now owns.
 */
import { createParser, parseAsString, parseAsStringLiteral } from "nuqs";
import type { JobStatus, SafetyDecisionType } from "@/api/types";

/** Free-text search input — empty string default. */
export const parseAsSearchTerm = parseAsString.withDefault("");

/**
 * 1-based page number with min-1 validation.
 *
 * Rejects `0`, negatives, and non-integer junk at the parser layer so
 * `parseServerSide` falls back to the default of 1. `clearOnDefault` keeps
 * shareable links short by stripping `?page=1`.
 */
export const parseAsPage = createParser({
  parse(value: string) {
    const n = Number.parseInt(value, 10);
    if (!Number.isFinite(n) || n < 1) return null;
    return n;
  },
  serialize(value: number) {
    return String(value);
  },
})
  .withDefault(1)
  .withOptions({ clearOnDefault: true });

/** Time-range bucket presets. 'custom' signals the caller will supply explicit from/to. */
export const TIME_RANGE_BUCKETS = ["1h", "24h", "7d", "30d", "custom"] as const;
export type TimeRangeBucket = (typeof TIME_RANGE_BUCKETS)[number];

/** Time-range parser without a baked-in default — chain `.withDefault('24h')` per page. */
export const parseAsTimeRange = parseAsStringLiteral(TIME_RANGE_BUCKETS);

/**
 * Canonical job-status tuple (mirrors `api/types.ts → JobStatus`).
 * Exported so call sites can also iterate the set when rendering filter chips.
 */
export const JOB_STATUSES = [
  "pending",
  "scheduled",
  "dispatched",
  "running",
  "succeeded",
  "failed",
  "cancelled",
  "approval_required",
  "denied",
  "timeout",
  "output_quarantined",
  "quarantined",
] as const satisfies readonly JobStatus[];

/**
 * Typed parser for the `?status=…` filter on Jobs / Audit / DLQ surfaces. No
 * baked-in default — chain `.withDefault('running')` (or whichever the page
 * needs) at the call site so distinct surfaces can pick distinct defaults.
 */
export const parseAsJobStatus = parseAsStringLiteral(JOB_STATUSES);

/**
 * Canonical safety-decision tuple (mirrors `api/types.ts → SafetyDecisionType`).
 * Used by Audit, Approvals, and Policy surfaces for the `?decision=…` filter.
 */
export const SAFETY_DECISIONS = [
  "allow",
  "deny",
  "require_approval",
  "allow_with_constraints",
  "throttle",
] as const satisfies readonly SafetyDecisionType[];

/**
 * Typed parser for the `?decision=…` filter. Same default-deferral pattern as
 * `parseAsJobStatus` — pages chain `.withDefault(...)` per surface.
 */
export const parseAsDecision = parseAsStringLiteral(SAFETY_DECISIONS);

/**
 * Typed enum parser with required default. Sugar for
 * `parseAsStringLiteral(values).withDefault(defaultValue)` so hero pages get a
 * single-line, default-aware filter parser for one-off enums that don't yet
 * deserve their own concrete export here.
 */
export function parseAsEnum<T extends string>(
  values: readonly T[],
  defaultValue: T,
) {
  return parseAsStringLiteral(values).withDefault(defaultValue);
}
