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
import { parseAsInteger, parseAsString, parseAsStringLiteral } from "nuqs";

/** Free-text search input — empty string default. */
export const parseAsSearchTerm = parseAsString.withDefault("");

/** 1-based page number; clears the URL param at the default to keep links short. */
export const parseAsPage = parseAsInteger
  .withDefault(1)
  .withOptions({ clearOnDefault: true });

/** Time-range bucket presets. 'custom' signals the caller will supply explicit from/to. */
export const TIME_RANGE_BUCKETS = ["1h", "24h", "7d", "30d", "custom"] as const;
export type TimeRangeBucket = (typeof TIME_RANGE_BUCKETS)[number];

/** Time-range parser without a baked-in default — chain `.withDefault('24h')` per page. */
export const parseAsTimeRange = parseAsStringLiteral(TIME_RANGE_BUCKETS);

/**
 * Typed enum parser with required default. Sugar for
 * `parseAsStringLiteral(values).withDefault(defaultValue)` so hero pages get a
 * single-line, default-aware filter parser.
 */
export function parseAsEnum<T extends string>(
  values: readonly T[],
  defaultValue: T,
) {
  return parseAsStringLiteral(values).withDefault(defaultValue);
}
