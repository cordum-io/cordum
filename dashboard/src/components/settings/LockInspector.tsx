import { useState } from "react";
import { Lock, Info, RefreshCw } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { Badge } from "../ui/Badge";
import { cn } from "../../lib/utils";
import { useAdminLocks } from "../../hooks/useAdminLocks";
import type { AdminLock } from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Typical max TTL per lock type (ms) — used to compute TTL bar percentage. */
const TYPE_MAX_TTL: Record<string, number> = {
  reconciler: 30_000,
  replayer: 30_000,
  job: 30_000,
  snapshot: 30_000,
  dlq_cleanup: 30_000,
  workflow_run: 30_000,
  delay_poller: 30_000,
  workflow_reconciler: 30_000,
  rate_limit: 60_000,
  jwks_cache: 21_600_000, // 6h
  circuit_breaker: 60_000,
  marketplace_cache: 300_000, // 5m
};

function ttlPercent(lock: AdminLock): number {
  const maxTtl = TYPE_MAX_TTL[lock.type] ?? 30_000;
  if (maxTtl <= 0) return 100;
  return Math.min(100, Math.max(0, (lock.ttl_remaining_ms / maxTtl) * 100));
}

function ttlVariant(pct: number): "success" | "warning" | "danger" {
  if (pct > 50) return "success";
  if (pct > 20) return "warning";
  return "danger";
}

const TTL_BAR_COLORS: Record<string, string> = {
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
};

function formatTtl(ms: number): string {
  if (ms <= 0) return "0s";
  const secs = Math.ceil(ms / 1000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.ceil(secs / 60);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  return `${hrs}h ${mins % 60}m`;
}

function shortenKey(key: string): string {
  // Show last 2 segments for readability
  const parts = key.split(":");
  if (parts.length <= 3) return key;
  return "..." + parts.slice(-2).join(":");
}

// ---------------------------------------------------------------------------
// Filter tabs
// ---------------------------------------------------------------------------

function TypeFilter({
  types,
  active,
  onChange,
}: {
  types: string[];
  active: string;
  onChange: (t: string) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1.5">
      <button
        type="button"
        className={cn(
          "rounded-full px-2.5 py-1 text-xs font-medium transition-colors",
          active === "all"
            ? "bg-accent text-white"
            : "bg-surface2 text-muted hover:text-ink",
        )}
        onClick={() => onChange("all")}
      >
        All
      </button>
      {types.map((t) => (
        <button
          key={t}
          type="button"
          className={cn(
            "rounded-full px-2.5 py-1 text-xs font-medium transition-colors",
            active === t
              ? "bg-accent text-white"
              : "bg-surface2 text-muted hover:text-ink",
          )}
          onClick={() => onChange(t)}
        >
          {t}
        </button>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Lock table row
// ---------------------------------------------------------------------------

function LockRow({ lock }: { lock: AdminLock }) {
  const pct = ttlPercent(lock);
  const variant = ttlVariant(pct);

  return (
    <tr className="border-t border-border/50 transition-colors hover:bg-surface2/20">
      <td className="px-4 py-2" title={lock.key}>
        <span className="text-xs font-mono text-ink">{shortenKey(lock.key)}</span>
      </td>
      <td className="px-4 py-2">
        <Badge variant="info" className="text-[10px]">{lock.type}</Badge>
      </td>
      <td className="px-4 py-2">
        <span className="text-xs font-mono text-muted">{lock.holder}</span>
      </td>
      <td className="px-4 py-2">
        <div className="flex items-center gap-2">
          <div className="h-1.5 w-16 rounded-full bg-surface2 overflow-hidden">
            <div
              className={cn("h-full rounded-full transition-all", TTL_BAR_COLORS[variant])}
              style={{ width: `${pct}%` }}
            />
          </div>
          <span className={cn("text-xs font-mono", `text-${variant}`)}>
            {formatTtl(lock.ttl_remaining_ms)}
          </span>
        </div>
      </td>
    </tr>
  );
}

// ---------------------------------------------------------------------------
// LockInspector (exported)
// ---------------------------------------------------------------------------

export function LockInspector() {
  const queryClient = useQueryClient();
  const { data, isLoading, isError, isFetching } = useAdminLocks();
  const [typeFilter, setTypeFilter] = useState("all");

  const locks = data?.locks ?? [];
  const types = [...new Set(locks.map((l) => l.type))].sort();
  const filtered = typeFilter === "all" ? locks : locks.filter((l) => l.type === typeFilter);

  // Loading skeleton
  if (isLoading) {
    return (
      <Card>
        <CardHeader>
          <div className="flex items-center gap-2">
            <Lock className="h-4 w-4 text-muted" />
            <CardTitle className="text-sm">Distributed Locks</CardTitle>
          </div>
        </CardHeader>
        <div className="space-y-2 animate-pulse">
          {Array.from({ length: 3 }, (_, i) => (
            <div key={i} className="h-8 rounded bg-surface2" />
          ))}
        </div>
      </Card>
    );
  }

  // Error state
  if (isError) {
    return (
      <Card>
        <CardHeader>
          <div className="flex items-center gap-2">
            <Lock className="h-4 w-4 text-muted" />
            <CardTitle className="text-sm">Distributed Locks</CardTitle>
          </div>
        </CardHeader>
        <div className="flex items-center justify-between py-4">
          <p className="text-sm text-danger">Failed to load locks.</p>
          <button
            type="button"
            onClick={() => queryClient.invalidateQueries({ queryKey: ["admin-locks"] })}
            className="rounded-lg px-3 py-1.5 text-xs font-medium text-accent hover:bg-surface2 transition-colors"
          >
            Retry
          </button>
        </div>
      </Card>
    );
  }

  // Empty state
  if (locks.length === 0) {
    return (
      <Card>
        <CardHeader>
          <div className="flex items-center gap-2">
            <Lock className="h-4 w-4 text-muted" />
            <CardTitle className="text-sm">Distributed Locks</CardTitle>
          </div>
        </CardHeader>
        <div className="flex items-center gap-2 text-sm text-muted">
          <Info className="h-4 w-4" />
          No active locks — normal for idle clusters
        </div>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2">
          <Lock className="h-4 w-4 text-muted" />
          <CardTitle className="text-sm">Distributed Locks</CardTitle>
          <Badge variant="default" className="text-[10px]">{locks.length}</Badge>
        </div>
        <button
          type="button"
          onClick={() => queryClient.invalidateQueries({ queryKey: ["admin-locks"] })}
          className="rounded-lg p-1.5 text-muted hover:text-ink hover:bg-surface2 transition-colors"
          title="Refresh now"
        >
          <RefreshCw className={cn("h-3.5 w-3.5", isFetching && "animate-spin")} />
        </button>
      </CardHeader>

      {types.length > 1 && (
        <div className="mb-3">
          <TypeFilter types={types} active={typeFilter} onChange={setTypeFilter} />
        </div>
      )}

      <div className="overflow-x-auto rounded-lg border border-border">
        <table className="w-full text-xs">
          <thead>
            <tr className="bg-surface2/30 text-left text-muted">
              <th className="px-4 py-2 font-medium">Key</th>
              <th className="px-4 py-2 font-medium">Type</th>
              <th className="px-4 py-2 font-medium">Holder</th>
              <th className="px-4 py-2 font-medium">TTL Remaining</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((lock) => (
              <LockRow key={lock.key} lock={lock} />
            ))}
          </tbody>
        </table>
      </div>

      <p className="mt-2 text-[10px] text-muted">
        Auto-refreshes every 5 seconds. Read-only.
      </p>
    </Card>
  );
}
