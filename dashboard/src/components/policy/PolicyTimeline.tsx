import { useState, useMemo } from "react";
import { Upload, RotateCcw, Pencil, Settings, ChevronDown, ChevronUp } from "lucide-react";
import { cn } from "../../lib/utils";
import type { PolicyAuditEntry } from "../../hooks/usePolicies";

// ---------------------------------------------------------------------------
// Categories
// ---------------------------------------------------------------------------

type AuditCategory = "publish" | "rollback" | "rule_change" | "config_change";

const CATEGORIES: {
  key: AuditCategory;
  label: string;
  icon: typeof Upload;
  dotColor: string;
  textColor: string;
}[] = [
  { key: "publish", label: "Publishes", icon: Upload, dotColor: "bg-success", textColor: "text-success" },
  { key: "rollback", label: "Rollbacks", icon: RotateCcw, dotColor: "bg-danger", textColor: "text-danger" },
  { key: "rule_change", label: "Rule Changes", icon: Pencil, dotColor: "bg-info", textColor: "text-info" },
  { key: "config_change", label: "Config Changes", icon: Settings, dotColor: "bg-warning", textColor: "text-warning" },
];

function categorize(action: string): AuditCategory {
  const lower = action.toLowerCase();
  if (lower.includes("publish")) return "publish";
  if (lower.includes("rollback") || lower.includes("restore")) return "rollback";
  if (lower.includes("rule")) return "rule_change";
  return "config_change";
}

function describeEntry(entry: PolicyAuditEntry): string {
  const action = entry.action.toLowerCase();
  const details = entry.details ?? {};
  const bundleIds = details.bundle_ids as string[] | undefined;
  const message = details.message as string | undefined;

  if (action.includes("publish")) {
    const ids = bundleIds?.join(", ") ?? entry.bundleId;
    return `Published bundle ${ids}${message ? `: ${message}` : ""}`;
  }
  if (action.includes("rollback") || action.includes("restore")) {
    return `Rolled back${message ? `: ${message}` : ""}`;
  }
  if (action.includes("rule_create") || action.includes("create")) {
    return `Rule created: ${entry.bundleId}`;
  }
  if (action.includes("rule_update") || action.includes("update")) {
    return `Rule updated: ${entry.bundleId}`;
  }
  if (action.includes("rule_delete") || action.includes("delete")) {
    return `Rule deleted: ${entry.bundleId}`;
  }
  return `${entry.action}${message ? `: ${message}` : ""}`;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function timeAgo(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1_000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

// ---------------------------------------------------------------------------
// Category filter checkboxes
// ---------------------------------------------------------------------------

function CategoryFilters({
  active,
  onChange,
}: {
  active: Set<AuditCategory>;
  onChange: (next: Set<AuditCategory>) => void;
}) {
  const allActive = active.size === CATEGORIES.length;
  const noneActive = active.size === 0;

  const toggleCategory = (cat: AuditCategory) => {
    const next = new Set(active);
    if (next.has(cat)) next.delete(cat);
    else next.add(cat);
    onChange(next);
  };

  const setAll = () => onChange(new Set(CATEGORIES.map((c) => c.key)));
  const setNone = () => onChange(new Set());

  return (
    <div className="flex flex-wrap items-center gap-3">
      {CATEGORIES.map(({ key, label, icon: Icon, textColor }) => (
        <label
          key={key}
          className={cn(
            "flex cursor-pointer items-center gap-1.5 text-xs font-medium transition",
            active.has(key) ? textColor : "text-muted-foreground opacity-50",
          )}
        >
          <input
            type="checkbox"
            checked={active.has(key)}
            onChange={() => toggleCategory(key)}
            className="accent-current"
          />
          <Icon className="h-3.5 w-3.5" />
          {label}
        </label>
      ))}
      <span className="text-xs text-muted-foreground">|</span>
      <button
        type="button"
        onClick={setAll}
        disabled={allActive}
        className="text-xs font-semibold text-accent hover:underline disabled:opacity-40"
      >
        All
      </button>
      <button
        type="button"
        onClick={setNone}
        disabled={noneActive}
        className="text-xs font-semibold text-accent hover:underline disabled:opacity-40"
      >
        None
      </button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Timeline entry
// ---------------------------------------------------------------------------

function TimelineEntry({ entry, category }: { entry: PolicyAuditEntry; category: AuditCategory }) {
  const [expanded, setExpanded] = useState(false);
  const cat = CATEGORIES.find((c) => c.key === category)!;
  const Icon = cat.icon;

  return (
    <div className="relative flex gap-4 pb-6 last:pb-0">
      {/* Vertical line */}
      <div className="absolute left-[11px] top-6 bottom-0 w-0.5 bg-border last:hidden" />

      {/* Dot + icon */}
      <div className="relative flex flex-col items-center">
        <div className={cn("h-6 w-6 rounded-full flex items-center justify-center", cat.dotColor)}>
          <Icon className="h-3 w-3 text-primary-foreground" />
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 min-w-0">
        <div className="flex items-start justify-between gap-2">
          <div>
            <p className="text-sm font-medium text-ink">
              {describeEntry(entry)}
            </p>
            <p className="text-xs text-muted-foreground">
              {entry.actor && <span>{entry.actor} &middot; </span>}
              <span title={new Date(entry.timestamp).toLocaleString()}>
                {timeAgo(entry.timestamp)}
              </span>
            </p>
          </div>
          <button
            type="button"
            onClick={() => setExpanded((v) => !v)}
            className="flex-shrink-0 rounded-full p-1 text-muted-foreground transition hover:bg-surface2 hover:text-ink"
          >
            {expanded ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
          </button>
        </div>

        {/* Expanded details */}
        {expanded && entry.details && (
          <pre className="mt-2 max-h-48 overflow-auto rounded-xl border border-border bg-surface2/30 p-2.5 text-xs text-muted-foreground">
            {JSON.stringify(entry.details, null, 2)}
          </pre>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// PolicyTimeline (main export)
// ---------------------------------------------------------------------------

interface PolicyTimelineProps {
  entries: PolicyAuditEntry[];
}

export function PolicyTimeline({ entries }: PolicyTimelineProps) {
  const [activeCategories, setActiveCategories] = useState<Set<AuditCategory>>(
    () => new Set(CATEGORIES.map((c) => c.key)),
  );

  // Categorize + filter + sort
  const categorized = useMemo(() => {
    return entries
      .map((e) => ({ entry: e, category: categorize(e.action) }))
      .filter(({ category }) => activeCategories.has(category))
      .sort((a, b) => new Date(b.entry.timestamp).getTime() - new Date(a.entry.timestamp).getTime());
  }, [entries, activeCategories]);

  return (
    <div className="space-y-4">
      {/* Category filter checkboxes */}
      <CategoryFilters active={activeCategories} onChange={setActiveCategories} />

      {/* Timeline */}
      {categorized.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted-foreground">
          No activity entries match the selected categories.
        </p>
      ) : (
        <div className="max-h-[600px] overflow-y-auto pr-1">
          {categorized.map(({ entry, category }) => (
            <TimelineEntry key={entry.id} entry={entry} category={category} />
          ))}
        </div>
      )}
    </div>
  );
}

