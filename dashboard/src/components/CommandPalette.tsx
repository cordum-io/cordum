import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Briefcase, GitBranch, Play, Package, Search } from "lucide-react";
import { Input } from "./ui/Input";
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

const TYPE_ORDER: SearchResult["type"][] = ["job", "workflow", "run", "pack"];

const TYPE_LABELS: Record<SearchResult["type"], string> = {
  job: "Jobs",
  workflow: "Workflows",
  run: "Runs",
  pack: "Packs",
};

function typeIcon(type: SearchResult["type"]) {
  switch (type) {
    case "job":
      return <Briefcase className="h-4 w-4 text-accent" />;
    case "workflow":
      return <GitBranch className="h-4 w-4 text-purple-500" />;
    case "run":
      return <Play className="h-4 w-4 text-success" />;
    case "pack":
      return <Package className="h-4 w-4 text-warning" />;
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
// Hook: debounced search
// ---------------------------------------------------------------------------

function useDebouncedSearch(query: string, delay: number) {
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
      get<SearchResponse>(`/search?q=${encodeURIComponent(trimmed)}`)
        .then((res) => setResults(res.data ?? []))
        .catch(() => setResults([]))
        .finally(() => setLoading(false));
    }, delay);

    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [query, delay]);

  return { results, loading };
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function CommandPalette() {
  const open = useUiStore((s) => s.commandOpen);
  const setOpen = useUiStore((s) => s.setCommandOpen);
  const navigate = useNavigate();

  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  const { results, loading } = useDebouncedSearch(query, 300);

  // Group results by type
  const grouped = TYPE_ORDER.map((type) => ({
    type,
    items: results.filter((r) => r.type === type),
  })).filter((g) => g.items.length > 0);

  // Flat list for keyboard navigation
  const flat = grouped.flatMap((g) => g.items);

  // Reset active index when results change
  useEffect(() => {
    setActiveIndex(0);
  }, [results]);

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

  const selectResult = useCallback(
    (result: SearchResult) => {
      close();
      navigate(resultPath(result));
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
      selectResult(flat[activeIndex]);
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

  let flatIdx = 0;

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
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search jobs, workflows, runs, packs..."
            className="w-full border-0 bg-transparent px-0 py-0 text-sm text-ink shadow-none outline-none placeholder:text-muted/60"
          />
          <kbd className="hidden shrink-0 rounded-md border border-border bg-surface2 px-1.5 py-0.5 text-[10px] font-semibold text-muted sm:block">
            ESC
          </kbd>
        </div>

        {/* Results */}
        <div ref={listRef} className="max-h-[50vh] overflow-y-auto p-2">
          {/* Loading */}
          {loading && (
            <p className="px-3 py-6 text-center text-xs text-muted">
              Searching...
            </p>
          )}

          {/* Empty */}
          {!loading && query.trim() && flat.length === 0 && (
            <p className="px-3 py-6 text-center text-xs text-muted">
              No results for &ldquo;{query.trim()}&rdquo;
            </p>
          )}

          {/* No query */}
          {!loading && !query.trim() && (
            <p className="px-3 py-6 text-center text-xs text-muted">
              Start typing to search...
            </p>
          )}

          {/* Grouped results */}
          {!loading &&
            grouped.map((group) => (
              <div key={group.type} className="mb-1">
                <p className="px-3 py-1.5 text-[10px] font-semibold uppercase tracking-widest text-muted">
                  {TYPE_LABELS[group.type]}
                </p>
                {group.items.map((item) => {
                  const idx = flatIdx++;
                  const isActive = idx === activeIndex;
                  return (
                    <button
                      key={item.id}
                      type="button"
                      data-active={isActive}
                      className={cn(
                        "flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left text-sm transition-colors",
                        isActive
                          ? "bg-accent/10 text-ink"
                          : "text-muted hover:bg-surface2 hover:text-ink",
                      )}
                      onClick={() => selectResult(item)}
                      onMouseEnter={() => setActiveIndex(idx)}
                    >
                      {typeIcon(item.type)}
                      <div className="min-w-0 flex-1">
                        <p className="truncate font-medium">{item.title}</p>
                        {item.subtitle && (
                          <p className="truncate text-xs text-muted">
                            {item.subtitle}
                          </p>
                        )}
                      </div>
                      <span className="shrink-0 text-[10px] text-muted/60">
                        {item.id.slice(0, 8)}
                      </span>
                    </button>
                  );
                })}
              </div>
            ))}
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
