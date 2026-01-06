import { useEffect, useMemo, useRef, useState } from "react";
import { cn } from "../lib/utils";
import { useUiStore } from "../state/ui";

type CommandItem = {
  id: string;
  title: string;
  description?: string;
  group?: string;
  onSelect: () => void;
};

export function CommandPalette({ items }: { items: CommandItem[] }) {
  const open = useUiStore((state) => state.commandOpen);
  const setOpen = useUiStore((state) => state.setCommandOpen);
  const [query, setQuery] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (open) {
      setQuery("");
      window.setTimeout(() => inputRef.current?.focus(), 10);
    }
  }, [open]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
      }
    };
    if (open) {
      window.addEventListener("keydown", onKey);
    }
    return () => window.removeEventListener("keydown", onKey);
  }, [open, setOpen]);

  const filtered = useMemo(() => {
    if (!query.trim()) {
      return items;
    }
    const q = query.toLowerCase();
    return items.filter((item) =>
      [item.title, item.description, item.group].some((value) => value?.toLowerCase().includes(q))
    );
  }, [items, query]);

  if (!open) {
    return null;
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 px-4 backdrop-blur-sm">
      <div className="glass-panel w-full max-w-2xl rounded-3xl p-6 shadow-lift">
        <input
          ref={inputRef}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Search or type a command"
          className="w-full rounded-2xl border border-border bg-white/80 px-4 py-3 text-sm font-semibold text-ink focus:outline-none focus:ring-2 focus:ring-[color:var(--ring)]"
        />
        <div className="mt-4 max-h-[360px] overflow-y-auto">
          {filtered.length === 0 ? (
            <div className="rounded-2xl border border-dashed border-border p-6 text-center text-sm text-muted">
              No matches. Try a different query.
            </div>
          ) : (
            filtered.map((item) => (
              <button
                key={item.id}
                type="button"
                onClick={() => {
                  item.onSelect();
                  setOpen(false);
                }}
                className={cn(
                  "mb-2 w-full rounded-2xl border border-transparent bg-white/70 px-4 py-3 text-left transition",
                  "hover:border-accent hover:bg-white"
                )}
              >
                <div className="flex items-center justify-between">
                  <div>
                    <div className="text-sm font-semibold text-ink">{item.title}</div>
                    {item.description ? (
                      <div className="text-xs text-muted">{item.description}</div>
                    ) : null}
                  </div>
                  {item.group ? (
                    <span className="rounded-full bg-[color:rgba(15,127,122,0.12)] px-3 py-1 text-[10px] font-semibold uppercase tracking-[0.2em] text-accent">
                      {item.group}
                    </span>
                  ) : null}
                </div>
              </button>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
