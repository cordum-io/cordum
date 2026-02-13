import { useCallback, useMemo, useRef } from "react";
import { useSearchParams } from "react-router-dom";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type FilterType = "string" | "string[]" | "number";

type FilterSchema = Record<string, FilterType>;

type FilterValues<S extends FilterSchema> = {
  [K in keyof S]: S[K] extends "string[]"
    ? string[]
    : S[K] extends "number"
      ? number | undefined
      : string;
};

interface UseUrlFiltersOptions {
  /** URL param keys that clearAll should NOT remove (e.g. "tab") */
  preserveParams?: string[];
  /** Reset "page" param to "1" on any filter change (default: true) */
  resetPage?: boolean;
}

type SetFilter<S extends FilterSchema> = <K extends keyof S & string>(
  key: K,
  value: FilterValues<S>[K],
) => void;

type SetFilterDebounced<S extends FilterSchema> = <K extends keyof S & string>(
  key: K,
  value: FilterValues<S>[K],
  delayMs?: number,
) => void;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function parseList(param: string | null): string[] {
  return param?.split(",").filter(Boolean) ?? [];
}

function serializeValue(type: FilterType, value: unknown): string {
  if (type === "string[]") {
    const arr = value as string[];
    return arr.length > 0 ? arr.join(",") : "";
  }
  if (type === "number") {
    return value != null ? String(value) : "";
  }
  return (value as string) ?? "";
}

function parseValue(type: FilterType, raw: string | null): unknown {
  if (type === "string[]") return parseList(raw);
  if (type === "number") {
    if (raw == null || raw === "") return undefined;
    const n = Number(raw);
    return Number.isNaN(n) ? undefined : n;
  }
  return raw ?? "";
}

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

export function useUrlFilters<S extends FilterSchema>(
  schema: S,
  options?: UseUrlFiltersOptions,
): [
  filters: FilterValues<S>,
  setFilter: SetFilter<S>,
  setFilterDebounced: SetFilterDebounced<S>,
  clearAll: () => void,
  activeCount: number,
] {
  const [searchParams, setSearchParams] = useSearchParams();
  const resetPage = options?.resetPage ?? true;
  const preserveParams = options?.preserveParams;

  // Stable refs for debounce timers keyed by filter name
  const timersRef = useRef<Record<string, ReturnType<typeof setTimeout>>>({});

  // Parse all filter values from URL
  const filters = useMemo(() => {
    const result = {} as Record<string, unknown>;
    for (const [key, type] of Object.entries(schema)) {
      result[key] = parseValue(type, searchParams.get(key));
    }
    return result as FilterValues<S>;
    // searchParams.toString() used as dep instead of object ref to prevent re-render loops
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams.toString()]);

  // Count active (non-empty) filters
  const activeCount = useMemo(() => {
    let count = 0;
    for (const [key, type] of Object.entries(schema)) {
      const raw = searchParams.get(key);
      if (type === "string[]") {
        if (parseList(raw).length > 0) count++;
      } else if (type === "number") {
        if (raw != null && raw !== "") count++;
      } else {
        if (raw) count++;
      }
    }
    return count;
    // searchParams.toString() used as dep instead of object ref to prevent re-render loops
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams.toString()]);

  // Set a single filter value immediately
  const setFilter = useCallback<SetFilter<S>>(
    (key, value) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        const type = schema[key];
        const serialized = serializeValue(type, value);
        if (serialized) {
          next.set(key, serialized);
        } else {
          next.delete(key);
        }
        if (resetPage && key !== "page") {
          next.set("page", "1");
        }
        return next;
      });
    },
    [setSearchParams, schema, resetPage],
  );

  // Set a filter value with debounce (default 400ms)
  const setFilterDebounced = useCallback<SetFilterDebounced<S>>(
    (key, value, delayMs = 400) => {
      clearTimeout(timersRef.current[key]);
      timersRef.current[key] = setTimeout(() => {
        setFilter(key, value);
      }, delayMs);
    },
    [setFilter],
  );

  // Clear all schema-managed params
  const clearAll = useCallback(() => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      for (const key of Object.keys(schema)) {
        next.delete(key);
      }
      // Also clear page
      next.delete("page");
      // Preserve specified params
      if (preserveParams) {
        for (const p of preserveParams) {
          const val = prev.get(p);
          if (val) next.set(p, val);
        }
      }
      return next;
    });
  }, [setSearchParams, schema, preserveParams]);

  return [filters, setFilter, setFilterDebounced, clearAll, activeCount];
}

/** @internal exported for unit tests */
export const __urlFiltersInternal = {
  parseList,
  serializeValue,
  parseValue,
};
