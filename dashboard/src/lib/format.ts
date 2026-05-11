export function formatCount(n: number): string {
  if (n >= 1_000_000) {
    return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
  }
  if (n >= 1_000) {
    return `${(n / 1_000).toFixed(1).replace(/\.0$/, "")}K`;
  }
  return String(n);
}

export { formatRelativeTime as formatRelative, formatDuration } from "./utils";

export function formatDateTime(dateStr: string): string {
  return new Date(dateStr).toLocaleString();
}

export interface FormatBytesOptions {
  /** String returned when value is missing, non-finite, negative, or
   * (when `zeroAsBytes` is false) zero. Default "—". */
  fallback?: string;
  /** Use IEC binary unit labels (KiB/MiB/GiB). Default false (KB/MB/GB). */
  iec?: boolean;
  /** Render values >= 1 GB at the GB tier. Default false (caps at MB). */
  includeGB?: boolean;
  /** When true, value=0 renders as "0 B" instead of `fallback`. Default false. */
  zeroAsBytes?: boolean;
}

/**
 * Human-readable byte sizes. Replaces 4 hand-rolled `formatBytes` copies
 * across `components/jobs/ArtifactPanel`, `components/edge/edgeArtifactUtils`,
 * `components/edge/EdgeEventInspector`, and `pages/settings/LicensePage`.
 *
 * Tiers: B / KB (1 decimal) / MB (2 decimals) / GB (1 decimal). The tier
 * boundaries are 1024 each — these are binary kilobytes ("KB"), not
 * SI kilobytes (1000). Pass `iec: true` to render the unambiguous IEC
 * labels (KiB/MiB/GiB) for consumers that want to be precise about the base.
 */
export function formatBytes(
  value: number | null | undefined,
  options: FormatBytesOptions = {},
): string {
  const { fallback = "—", iec = false, includeGB = false, zeroAsBytes = false } = options;
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) return fallback;
  if (value === 0) return zeroAsBytes ? "0 B" : fallback;
  const KB = iec ? "KiB" : "KB";
  const MB = iec ? "MiB" : "MB";
  const GB = iec ? "GiB" : "GB";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} ${KB}`;
  if (!includeGB || value < 1024 * 1024 * 1024) {
    return `${(value / (1024 * 1024)).toFixed(2)} ${MB}`;
  }
  return `${(value / (1024 * 1024 * 1024)).toFixed(1)} ${GB}`;
}
