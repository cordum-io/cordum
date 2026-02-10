import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useLocation } from "react-router-dom";
import {
  Briefcase,
  Clock,
  GitBranch,
  Package,
  Play,
  Plus,
  Search,
  Settings,
  ShieldCheck,
  Sun,
  Zap,
} from "lucide-react";
import { useUiStore } from "../state/ui";
import { get } from "../api/client";
import { cn } from "../lib/utils";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface SearchResult {
  id: string;
  type: "job" | "workflow" | "run" | "pack";
  title: string;
  subtitle?: string;
}

interface SearchResponse {
  data: SearchResult[];
}

interface QuickAction {
  id: string;
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  shortcut?: string;
  path?: string;
  execute?: () => void;
}

interface RecentItem {
  id: string;
  type: string;
  title: string;
  path: string;
  timestamp: number;
}

type PaletteEntry =
  | { kind: "recent"; recent: RecentItem }
  | { kind: "action"; action: QuickAction }
  | { kind: "result"; result: SearchResult };

const TYPE_ORDER: SearchResult["type"][] = ["job", "workflow", "run", "pack"];

const TYPE_LABELS: Record<SearchResult["type"], string> = {
  job: "Jobs",
  workflow: "Workflows",
  run: "Runs",
  pack: "Packs",
};

// ---------------------------------------------------------------------------
// Scope detection
// ---------------------------------------------------------------------------

interface ScopeConfig {
  type: SearchResult["type"];
  label: string;
}

const SCOPE_MAP: Record<string, ScopeConfig> = {
  "/jobs": { type: "job", label: "Jobs" },
  "/workflows": { type: "workflow", label: "Workflows" },
  "/packs": { type: "pack", label: "Packs" },
};

function detectScope(pathname: string): ScopeConfig | null {
  for (const [prefix, config] of Object.entries(SCOPE_MAP)) {
    if (pathname === prefix || pathname.startsWith(prefix + "/")) {
      return config;
    }
  }
  return null;
}

function typeIcon(type: string) {
  switch (type) {
    case "job":
      return <Briefcase className="h-4 w-4 text-accent" />;
    case "workflow":
      return <GitBranch className="h-4 w-4 text-purple-500" />;
    case "run":
      return <Play className="h-4 w-4 text-success" />;
    case "pack":
      return <Package className="h-4 w-4 text-warning" />;
    default:
      return <Briefcase className="h-4 w-4 text-muted" />;
  }
}

function resultPath(result: SearchResult): string {
  switch (result.type) {
    case "job":
      return `/jobs/${result.id}`;
    case "workflow":
      return `/workflows/${result.id}`;
    case "run":
      return `/workflows`;
    case "pack":
      return `/packs`;
  }
}

// ---------------------------------------------------------------------------
// Recent items storage
// ---------------------------------------------------------------------------

const RECENT_KEY = "cordum-recent-items";
const MAX_RECENT = 10;

function loadRecentItems(): RecentItem[] {
  try {
    const raw = localStorage.getItem(RECENT_KEY);
    if (!raw) return [];
    const items = JSON.parse(raw) as RecentItem[];
    return Array.isArray(items) ? items.slice(0, MAX_RECENT) : [];
  } catch {
    return [];
  }
}

function saveRecentItem(item: RecentItem): void {
  try {
    const items = loadRecentItems().filter((r) => r.path !== item.path);
    items.unshift({ ...item, timestamp: Date.now() });
    localStorage.setItem(RECENT_KEY, JSON.stringify(items.slice(0, MAX_RECENT)));
  } catch {
    // ignore
  }
}

// ---------------------------------------------------------------------------
// Hook: debounced search
// ---------------------------------------------------------------------------

function useDebouncedSearch(query: string, delay: number, scopeType?: string) {
  const [results, setResults] = useState<SearchResult[]>([]);
  const [loading, setLoading] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (timerRef.current) clearTimeout(timerRef.current);

    const trimmed = query.trim();
    if (!trimmed) {
      setResults([]);
      setLoading(false);
      return;
    }

    setLoading(true);
    timerRef.current = setTimeout(() => {
      let url = `/search?q=${encodeURIComponent(trimmed)}`;
      if (scopeType) url += `&type=${encodeURIComponent(scopeType)}`;
      get<SearchResponse>(url)
        .then((res) => setResults(res.data ?? []))
        .catch(() => setResults([]))
        .finally(() => setLoading(false));
    }, delay);

    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [query, delay, scopeType]);

  return { results, loading };
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

function entryKey(entry: PaletteEntry): string {
  switch (entry.kind) {
    case "recent":
      return `rc-${entry.recent.path}`;
    case "action":
      return `a-${entry.action.id}`;
    case "result":
      return `r-${entry.result.id}`;
  }
}

export function CommandPalette() {
  const open = useUiStore((s) => s.commandOpen);
  const setOpen = useUiStore((s) => s.setCommandOpen);
  const toggleTheme = useUiStore((s) => s.toggleTheme);
  const navigate = useNavigate();
  const location = useLocation();

  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const [recentItems, setRecentItems] = useState<RecentItem[]>([]);
  const [isScoped, setIsScoped] = useState(true);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  const pageScope = detectScope(location.pathname);
  const activeScope = isScoped ? pageScope : null;

  const { results, loading } = useDebouncedSearch(query, 300, activeScope?.type);

  // Quick actions
  const quickActions = useMemo<QuickAction[]>(
    () => [
      { id: "nav-jobs", icon: Briefcase, label: "Go to Jobs", shortcut: "G J", path: "/jobs" },
      { id: "nav-workflows", icon: GitBranch, label: "Go to Workflows", shortcut: "G W", path: "/workflows" },
      { id: "nav-approvals", icon: ShieldCheck, label: "Go to Approvals", shortcut: "G A", path: "/approvals" },
      { id: "create-workflow", icon: Plus, label: "Create Workflow", path: "/workflows/new" },
      { id: "toggle-theme", icon: Sun, label: "Toggle Theme", execute: toggleTheme },
      { id: "nav-settings", icon: Settings, label: "Open Settings", shortcut: "G S", path: "/settings" },
    ],
    [toggleTheme],
  );

  // Load recent items and reset scope when palette opens
  useEffect(() => {
    if (open) {
      setRecentItems(loadRecentItems());
      setIsScoped(true);
    }
  }, [open]);

  // Build flat list for keyboard navigation
  const flat = useMemo<PaletteEntry[]>(() => {
    const trimmed = query.trim().toLowerCase();

    if (!trimmed) {
      const entries: PaletteEntry[] = [];
      for (const r of recentItems) entries.push({ kind: "recent", recent: r });
      for (const a of quickActions) entries.push({ kind: "action", action: a });
      return entries;
    }

    // Non-empty query: matching actions + search results (grouped by type order)
    const entries: PaletteEntry[] = [];
    const matchingActions = quickActions.filter((a) =>
      a.label.toLowerCase().includes(trimmed),
    );
    for (const a of matchingActions) entries.push({ kind: "action", action: a });
    for (const type of TYPE_ORDER) {
      for (const r of results) {
        if (r.type === type) entries.push({ kind: "result", result: r });
      }
    }
    return entries;
  }, [query, recentItems, quickActions, results]);

  // Reset active index when flat list changes
  useEffect(() => {
    setActiveIndex(0);
  }, [flat]);

  // Focus input when opened
  useEffect(() => {
    if (open) {
      setQuery("");
      setActiveIndex(0);
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);

  // Global keyboard shortcut
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setOpen(!open);
      }
    }
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [open, setOpen]);

  const close = useCallback(() => {
    setOpen(false);
    setQuery("");
  }, [setOpen]);

  const selectEntry = useCallback(
    (entry: PaletteEntry) => {
      close();
      switch (entry.kind) {
        case "result": {
          const path = resultPath(entry.result);
          saveRecentItem({
            id: entry.result.id,
            type: entry.result.type,
            title: entry.result.title,
            path,
            timestamp: Date.now(),
          });
          navigate(path);
          break;
        }
        case "recent":
          saveRecentItem(entry.recent);
          navigate(entry.recent.path);
          break;
        case "action":
          if (entry.action.path) {
            saveRecentItem({
              id: entry.action.id,
              type: "action",
              title: entry.action.label,
              path: entry.action.path,
              timestamp: Date.now(),
            });
            navigate(entry.action.path);
          } else if (entry.action.execute) {
            entry.action.execute();
          }
          break;
      }
    },
    [close, navigate],
  );

  // Keyboard navigation inside palette
  function handleKeyDown(e: React.KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      close();
      return;
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActiveIndex((prev) => Math.min(prev + 1, flat.length - 1));
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setActiveIndex((prev) => Math.max(prev - 1, 0));
      return;
    }
    if (e.key === "Enter" && flat[activeIndex]) {
      e.preventDefault();
      selectEntry(flat[activeIndex]);
    }
  }

  // Scroll active item into view
  useEffect(() => {
    if (!listRef.current) return;
    const active = listRef.current.querySelector("[data-active='true']");
    if (active) {
      active.scrollIntoView({ block: "nearest" });
    }
  }, [activeIndex]);

  if (!open) return null;

  // -------------------------------------------------------------------------
  // Render helpers
  // -------------------------------------------------------------------------

  const trimmed = query.trim().toLowerCase();
  const recentEntries = flat.filter(
    (e): e is Extract<PaletteEntry, { kind: "recent" }> => e.kind === "recent",
  );
  const actionEntries = flat.filter(
    (e): e is Extract<PaletteEntry, { kind: "action" }> => e.kind === "action",
  );
  const resultEntries = flat.filter(
    (e): e is Extract<PaletteEntry, { kind: "result" }> => e.kind === "result",
  );
  const resultGrouped = TYPE_ORDER.map((type) => ({
    type,
    items: resultEntries.filter((e) => e.result.type === type),
  })).filter((g) => g.items.length > 0);

  let flatIdx = 0;

  function renderItem(entry: PaletteEntry) {
    const idx = flatIdx++;
    const isActive = idx === activeIndex;

    let icon: React.ReactNode;
    let label: string;
    let subtitle: string | undefined;
    let trailing: React.ReactNode = null;

    switch (entry.kind) {
      case "recent":
        icon = typeIcon(entry.recent.type);
        label = entry.recent.title;
        subtitle = entry.recent.path;
        break;
      case "action": {
        const Icon = entry.action.icon;
        icon = <Icon className="h-4 w-4 text-accent" />;
        label = entry.action.label;
        if (entry.action.shortcut) {
          trailing = (
            <kbd className="shrink-0 rounded border border-border bg-surface2 px-1.5 py-0.5 text-[10px] font-mono text-muted">
              {entry.action.shortcut}
            </kbd>
          );
        }
        break;
      }
      case "result":
        icon = typeIcon(entry.result.type);
        label = entry.result.title;
        subtitle = entry.result.subtitle;
        trailing = (
          <span className="shrink-0 text-[10px] text-muted/60">
            {entry.result.id.slice(0, 8)}
          </span>
        );
        break;
    }

    return (
      <button
        key={entryKey(entry)}
        type="button"
        data-active={isActive}
        className={cn(
          "flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left text-sm transition-colors",
          isActive
            ? "bg-accent/10 text-ink"
            : "text-muted hover:bg-surface2 hover:text-ink",
        )}
        onClick={() => selectEntry(entry)}
        onMouseEnter={() => setActiveIndex(idx)}
      >
        {icon}
        <div className="min-w-0 flex-1">
          <p className="truncate font-medium">{label}</p>
          {subtitle && (
            <p className="truncate text-xs text-muted">{subtitle}</p>
          )}
        </div>
        {trailing}
      </button>
    );
  }

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[15vh]">
      {/* Backdrop */}
      <button
        type="button"
        aria-label="Close"
        onClick={close}
        className="absolute inset-0 bg-black/30 backdrop-blur-sm animate-fade-in"
      />

      {/* Dialog */}
      <div
        className="relative w-full max-w-lg rounded-2xl border border-border bg-white shadow-2xl animate-slide-in"
        role="dialog"
        aria-label="Command palette"
        onKeyDown={handleKeyDown}
      >
        {/* Search input */}
        <div className="flex items-center gap-3 border-b border-border px-4 py-3">
          <Search className="h-4 w-4 shrink-0 text-muted" />
          {activeScope && (
            <button
              type="button"
              onClick={() => setIsScoped(false)}
              className="shrink-0 rounded-full bg-accent/15 px-2 py-0.5 text-[10px] font-semibold text-accent transition hover:bg-accent/25"
              title="Click to search all"
            >
              {activeScope.label} &times;
            </button>
          )}
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={activeScope ? `Search ${activeScope.label.toLowerCase()}...` : "Search jobs, workflows, runs, packs..."}
            className="w-full border-0 bg-transparent px-0 py-0 text-sm text-ink shadow-none outline-none placeholder:text-muted/60"
          />
          {!isScoped && pageScope && (
            <button
              type="button"
              onClick={() => setIsScoped(true)}
              className="shrink-0 whitespace-nowrap text-[10px] text-muted transition hover:text-accent"
            >
              Search in {pageScope.label}
            </button>
          )}
          <kbd className="hidden shrink-0 rounded-md border border-border bg-surface2 px-1.5 py-0.5 text-[10px] font-semibold text-muted sm:block">
            ESC
          </kbd>
        </div>

        {/* Content */}
        <div ref={listRef} className="max-h-[50vh] overflow-y-auto p-2">
          {/* Loading */}
          {loading && (
            <p className="px-3 py-6 text-center text-xs text-muted">
              Searching...
            </p>
          )}

          {/* No results for search query */}
          {!loading && trimmed && flat.length === 0 && (
            <p className="px-3 py-6 text-center text-xs text-muted">
              No results for &ldquo;{query.trim()}&rdquo;
            </p>
          )}

          {/* Empty query: Recent Items + Quick Actions */}
          {!loading && !trimmed && (
            <>
              {recentEntries.length > 0 && (
                <div className="mb-1">
                  <p className="flex items-center gap-1 px-3 py-1.5 text-[10px] font-semibold uppercase tracking-widest text-muted">
                    <Clock className="h-3 w-3" />
                    Recent
                  </p>
                  {recentEntries.map((e) => renderItem(e))}
                </div>
              )}
              {actionEntries.length > 0 && (
                <div className="mb-1">
                  <p className="flex items-center gap-1 px-3 py-1.5 text-[10px] font-semibold uppercase tracking-widest text-muted">
                    <Zap className="h-3 w-3" />
                    Quick Actions
                  </p>
                  {actionEntries.map((e) => renderItem(e))}
                </div>
              )}
            </>
          )}

          {/* Search query: matching actions + grouped results */}
          {!loading && trimmed && (
            <>
              {actionEntries.length > 0 && (
                <div className="mb-1">
                  <p className="px-3 py-1.5 text-[10px] font-semibold uppercase tracking-widest text-muted">
                    Quick Actions
                  </p>
                  {actionEntries.map((e) => renderItem(e))}
                </div>
              )}
              {resultGrouped.map((group) => (
                <div key={group.type} className="mb-1">
                  <p className="px-3 py-1.5 text-[10px] font-semibold uppercase tracking-widest text-muted">
                    {TYPE_LABELS[group.type]}
                  </p>
                  {group.items.map((e) => renderItem(e))}
                </div>
              ))}
            </>
          )}
        </div>

        {/* Footer hint */}
        {flat.length > 0 && (
          <div className="flex items-center gap-4 border-t border-border px-4 py-2 text-[10px] text-muted">
            <span>
              <kbd className="rounded border border-border bg-surface2 px-1 py-0.5 font-mono">
                &uarr;&darr;
              </kbd>{" "}
              navigate
            </span>
            <span>
              <kbd className="rounded border border-border bg-surface2 px-1 py-0.5 font-mono">
                &crarr;
              </kbd>{" "}
              select
            </span>
            <span>
              <kbd className="rounded border border-border bg-surface2 px-1 py-0.5 font-mono">
                esc
              </kbd>{" "}
              close
            </span>
          </div>
        )}
      </div>
    </div>
  );
}
