import type { JobState } from "./api";

export function formatUnixSeconds(unixSeconds: number | undefined): string {
  if (!unixSeconds) {
    return "-";
  }
  // Some APIs accidentally return unix millis. Heuristically normalize.
  const seconds = unixSeconds > 1_000_000_000_000 ? Math.floor(unixSeconds / 1000) : unixSeconds;
  const dt = new Date(seconds * 1000);
  return new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(dt);
}

export function formatUnixMillis(unixMillis: number | undefined): string {
  if (!unixMillis) {
    return "-";
  }
  // Some APIs accidentally return unix seconds. Heuristically normalize.
  const millis = unixMillis < 1_000_000_000_000 ? unixMillis * 1000 : unixMillis;
  const dt = new Date(millis);
  return new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(dt);
}

export function formatRFC3339(value: string | null | undefined): string {
  if (!value) {
    return "-";
  }
  const dt = new Date(value);
  if (Number.isNaN(dt.getTime())) {
    return value;
  }
  return new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(dt);
}

export function jobStateLabel(state: JobState | string | undefined): string {
  return (state || "UNKNOWN").toString().toUpperCase();
}
