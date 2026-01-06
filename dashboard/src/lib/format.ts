export function formatDateTime(value?: string | number | Date): string {
  if (!value) {
    return "-";
  }
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "-";
  }
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

export function formatShortDate(value?: string | number | Date): string {
  if (!value) {
    return "-";
  }
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "-";
  }
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

export function formatDuration(start?: string | Date | null, end?: string | Date | null): string {
  if (!start) {
    return "-";
  }
  const startDate = start instanceof Date ? start : new Date(start);
  const endDate = end ? (end instanceof Date ? end : new Date(end)) : new Date();
  if (Number.isNaN(startDate.getTime()) || Number.isNaN(endDate.getTime())) {
    return "-";
  }
  const diffMs = Math.max(0, endDate.getTime() - startDate.getTime());
  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${minutes}m ${seconds % 60}s`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours}h ${minutes % 60}m`;
  }
  const days = Math.floor(hours / 24);
  return `${days}d ${hours % 24}h`;
}

export function formatRelative(value?: string | number | Date): string {
  if (!value) {
    return "-";
  }
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "-";
  }
  const diffMs = Date.now() - date.getTime();
  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 30) {
    return "just now";
  }
  if (seconds < 60) {
    return `${seconds}s ago`;
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${minutes}m ago`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours}h ago`;
  }
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

export function formatCount(value: number | undefined): string {
  if (value === undefined || Number.isNaN(value)) {
    return "-";
  }
  return new Intl.NumberFormat(undefined, { notation: "compact" }).format(value);
}

export function formatPercent(value: number | undefined): string {
  if (value === undefined || Number.isNaN(value)) {
    return "-";
  }
  return `${Math.round(value)}%`;
}

export function epochToMillis(value?: number): number | null {
  if (!value || Number.isNaN(value)) {
    return null;
  }
  if (value > 1e14) {
    return Math.floor(value / 1000);
  }
  if (value > 1e11) {
    return value;
  }
  return value * 1000;
}
