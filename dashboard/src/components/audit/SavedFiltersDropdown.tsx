import { useState, useCallback, useRef, useEffect } from "react";
import { Bookmark, ChevronDown, X, Save } from "lucide-react";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import {
  loadSavedFilters,
  saveSavedFilter,
  deleteSavedFilter,
  updateSavedFilter,
  generateFilterId,
  summarizeFilters,
} from "../../lib/audit-filters";
import type { SavedAuditFilter, SerializedFilterState } from "../../lib/audit-filters";
import type { AuditFilters } from "../../hooks/useAudit";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function filtersToSerialized(f: AuditFilters): SerializedFilterState {
  return {
    eventType: f.eventType,
    actor: f.actor,
    resourceType: f.resourceType,
    resourceId: f.resourceId,
    timeRange: f.timeRange,
    severity: f.severity,
    outcome: f.outcome,
    search: f.search,
  };
}

function hasActiveFilters(f: AuditFilters): boolean {
  return !!(
    f.eventType?.length ||
    f.actor ||
    f.resourceType ||
    f.resourceId ||
    f.severity?.length ||
    f.outcome?.length ||
    f.timeRange ||
    f.search
  );
}

// ---------------------------------------------------------------------------
// SavedFiltersDropdown
// ---------------------------------------------------------------------------

interface SavedFiltersDropdownProps {
  currentFilters: AuditFilters;
  activeFilterId: string | null;
  onLoad: (filters: SerializedFilterState, id: string) => void;
  onClearActive: () => void;
}

export function SavedFiltersDropdown({
  currentFilters,
  activeFilterId,
  onLoad,
  onClearActive,
}: SavedFiltersDropdownProps) {
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState("");
  const [filters, setFilters] = useState<SavedAuditFilter[]>([]);
  const menuRef = useRef<HTMLDivElement>(null);

  // Refresh on open
  useEffect(() => {
    if (open || saving) setFilters(loadSavedFilters());
  }, [open, saving]);

  // Outside click
  useEffect(() => {
    if (!open && !saving) return;
    const handler = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setOpen(false);
        setSaving(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open, saving]);

  const handleLoad = useCallback(
    (filter: SavedAuditFilter) => {
      onLoad(filter.filters, filter.id);
      setOpen(false);
    },
    [onLoad],
  );

  const handleDelete = useCallback(
    (id: string, e: React.MouseEvent) => {
      e.stopPropagation();
      deleteSavedFilter(id);
      setFilters(loadSavedFilters());
      if (activeFilterId === id) onClearActive();
    },
    [activeFilterId, onClearActive],
  );

  const handleSave = useCallback(() => {
    if (!name.trim()) return;
    const serialized = filtersToSerialized(currentFilters);

    if (activeFilterId && !filters.find((f) => f.id === activeFilterId)?.builtIn) {
      updateSavedFilter(activeFilterId, { name: name.trim(), filters: serialized });
    } else {
      saveSavedFilter({
        id: generateFilterId(),
        name: name.trim(),
        filters: serialized,
        createdAt: new Date().toISOString(),
      });
    }
    setName("");
    setSaving(false);
    setFilters(loadSavedFilters());
  }, [name, currentFilters, activeFilterId, filters]);

  const builtIn = filters.filter((f) => f.builtIn);
  const userFilters = filters.filter((f) => !f.builtIn);
  const showActive = hasActiveFilters(currentFilters);
  const activeFilter = activeFilterId ? filters.find((f) => f.id === activeFilterId) : null;

  return (
    <div className="relative inline-flex items-center gap-2" ref={menuRef}>
      {/* Active filter label */}
      {activeFilter && (
        <span className="flex items-center gap-1 rounded-lg bg-accent/10 px-2.5 py-1 text-xs font-medium text-accent">
          <Bookmark className="h-3 w-3" />
          {activeFilter.name}
          <button
            type="button"
            className="ml-1 rounded p-0.5 hover:bg-accent/20 transition"
            onClick={onClearActive}
          >
            <X className="h-3 w-3" />
          </button>
        </span>
      )}

      {/* Dropdown trigger */}
      <Button variant="outline" size="sm" onClick={() => { setOpen((v) => !v); setSaving(false); }}>
        <Bookmark className="h-3.5 w-3.5" />
        Saved
        {userFilters.length > 0 && (
          <span className="ml-1 rounded-full bg-accent/15 px-1.5 text-[10px] font-semibold text-accent">
            {userFilters.length}
          </span>
        )}
        <ChevronDown className="h-3 w-3" />
      </Button>

      {/* Save button */}
      {showActive && !saving && (
        <Button variant="ghost" size="sm" onClick={() => { setSaving(true); setOpen(false); setName(activeFilter?.name ?? ""); }}>
          <Save className="h-3.5 w-3.5" />
          {activeFilter && !activeFilter.builtIn ? "Update" : "Save filter"}
        </Button>
      )}

      {/* Save form */}
      {saving && (
        <div className="absolute left-0 top-full z-20 mt-1 w-72 rounded-xl border border-border bg-card p-3 shadow-lg space-y-2">
          <Input
            className="h-8 text-xs"
            placeholder="e.g., Q3 Governance Review"
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoFocus
            onKeyDown={(e) => { if (e.key === "Enter") handleSave(); if (e.key === "Escape") setSaving(false); }}
          />
          <div className="flex items-center justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={() => setSaving(false)}>Cancel</Button>
            <Button size="sm" disabled={!name.trim()} onClick={handleSave}>Save</Button>
          </div>
        </div>
      )}

      {/* Dropdown list */}
      {open && (
        <div className="absolute left-0 top-full z-20 mt-1 w-72 overflow-hidden rounded-xl border border-border bg-card shadow-lg">
          {builtIn.length > 0 && (
            <>
              <div className="px-3 pt-2.5 pb-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                Built-in
              </div>
              {builtIn.map((f) => (
                <button
                  key={f.id}
                  type="button"
                  className="flex w-full items-center gap-2 px-3 py-2 text-left transition hover:bg-surface2/60"
                  onClick={() => handleLoad(f)}
                >
                  <div className="min-w-0 flex-1">
                    <p className="text-xs font-medium text-ink">{f.name}</p>
                    <p className="truncate text-[10px] text-muted-foreground">{summarizeFilters(f.filters)}</p>
                  </div>
                </button>
              ))}
            </>
          )}
          {userFilters.length > 0 && (
            <>
              <div className="border-t border-border px-3 pt-2.5 pb-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                Your filters
              </div>
              {userFilters.map((f) => (
                <button
                  key={f.id}
                  type="button"
                  className="flex w-full items-center gap-2 px-3 py-2 text-left transition hover:bg-surface2/60"
                  onClick={() => handleLoad(f)}
                >
                  <div className="min-w-0 flex-1">
                    <p className="text-xs font-medium text-ink">{f.name}</p>
                    <p className="truncate text-[10px] text-muted-foreground">{summarizeFilters(f.filters)}</p>
                  </div>
                  <button
                    type="button"
                    className="shrink-0 rounded p-1 text-muted-foreground hover:text-danger transition"
                    onClick={(e) => handleDelete(f.id, e)}
                  >
                    <X className="h-3 w-3" />
                  </button>
                </button>
              ))}
            </>
          )}
          {filters.length === 0 && (
            <p className="px-3 py-4 text-center text-xs text-muted-foreground">No saved filters yet.</p>
          )}
        </div>
      )}
    </div>
  );
}
