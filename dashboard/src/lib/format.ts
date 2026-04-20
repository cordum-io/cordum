export function formatCount(n: number): string {
  if (n >= 1_000_000) {
    return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
  }
  if (n >= 1_000) {
    return `${(n / 1_000).toFixed(1).replace(/\.0$/, "")}K`;
  }
  return String(n);
}

export function formatStatusToken(value: string | undefined, fallback = "unknown"): string {
  const raw = (value ?? fallback).trim();
  if (!raw) return fallback;
  return raw.replace(/[_-]+/g, " ");
}

export function formatMonoId(value: string | undefined, visible = 12): string {
  if (!value) return "\u2014";
  if (value.length <= visible) return value;
  return value.slice(0, visible);
}
