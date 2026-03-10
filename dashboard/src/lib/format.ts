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

export function formatShortDate(dateStr: string): string {
  return new Date(dateStr).toLocaleDateString();
}

export function formatPercent(n: number): string {
  return `${(n * 100).toFixed(1)}%`;
}

export function epochToMillis(epoch: number): number {
  // If epoch is in seconds (< 1e12), convert to milliseconds
  return epoch < 1e12 ? epoch * 1000 : epoch;
}
