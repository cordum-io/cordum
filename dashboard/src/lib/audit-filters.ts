// ---------------------------------------------------------------------------
// Saved audit filter presets — localStorage persistence
// ---------------------------------------------------------------------------

import { generateUUID } from "./uuid";
import { safeLocalStorage } from "./storage";

const STORAGE_KEY = "cordum:audit:savedFilters";

export interface SerializedFilterState {
  eventType?: string[];
  actor?: string;
  resourceType?: string;
  resourceId?: string;
  timeRange?: string;
  severity?: string[];
  outcome?: string[];
  search?: string;
}

export interface SavedAuditFilter {
  id: string;
  name: string;
  description?: string;
  filters: SerializedFilterState;
  builtIn?: boolean;
  createdAt: string;
}

// ---------------------------------------------------------------------------
// Built-in presets (always present, not deletable)
// ---------------------------------------------------------------------------

const BUILT_IN_PRESETS: SavedAuditFilter[] = [
  {
    id: "__builtin_safety",
    name: "All Safety Decisions",
    filters: {
      eventType: ["evaluate", "allow", "deny", "approve", "throttle"],
    },
    builtIn: true,
    createdAt: "2026-01-01T00:00:00.000Z",
  },
  {
    id: "__builtin_high_sev",
    name: "High Severity",
    filters: {
      severity: ["high"],
    },
    builtIn: true,
    createdAt: "2026-01-01T00:00:00.000Z",
  },
  {
    id: "__builtin_recent_changes",
    name: "Recent Changes",
    filters: {
      eventType: ["edit", "create", "delete", "set", "change_password"],
      timeRange: "24h",
    },
    builtIn: true,
    createdAt: "2026-01-01T00:00:00.000Z",
  },
];

// ---------------------------------------------------------------------------
// Storage helpers
// ---------------------------------------------------------------------------

function readUserFilters(): SavedAuditFilter[] {
  try {
    const raw = safeLocalStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed as SavedAuditFilter[];
  } catch {
    return [];
  }
}

function writeUserFilters(filters: SavedAuditFilter[]): void {
  safeLocalStorage.setItem(STORAGE_KEY, JSON.stringify(filters));
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

export function loadSavedFilters(): SavedAuditFilter[] {
  return [...BUILT_IN_PRESETS, ...readUserFilters()];
}

export function saveSavedFilter(filter: SavedAuditFilter): void {
  const existing = readUserFilters();
  existing.push(filter);
  writeUserFilters(existing);
}

export function deleteSavedFilter(id: string): void {
  if (BUILT_IN_PRESETS.some((p) => p.id === id)) return;
  const existing = readUserFilters().filter((f) => f.id !== id);
  writeUserFilters(existing);
}

export function updateSavedFilter(
  id: string,
  updates: Partial<Pick<SavedAuditFilter, "name" | "description" | "filters">>,
): void {
  if (BUILT_IN_PRESETS.some((p) => p.id === id)) return;
  const existing = readUserFilters();
  const idx = existing.findIndex((f) => f.id === id);
  if (idx === -1) return;
  existing[idx] = { ...existing[idx], ...updates };
  writeUserFilters(existing);
}

export function generateFilterId(): string {
  return generateUUID();
}

export function summarizeFilters(filters: SerializedFilterState): string {
  const parts: string[] = [];
  if (filters.eventType?.length) parts.push(filters.eventType.slice(0, 3).join(", "));
  if (filters.severity?.length) parts.push(filters.severity.join(", ") + " severity");
  if (filters.actor) parts.push(`actor: ${filters.actor}`);
  if (filters.resourceType) parts.push(filters.resourceType);
  if (filters.timeRange) parts.push(filters.timeRange);
  if (filters.search) parts.push(`"${filters.search}"`);
  return parts.join(" · ") || "No filters";
}
