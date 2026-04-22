import { useState, useRef, useEffect } from "react";
import { MoreHorizontal, GitCompare, History, CopyCheck } from "lucide-react";
import { cn } from "@/lib/utils";

export type BundleTab = "yaml" | "preview" | "diff" | "history" | "shadow";

interface BundleDetailTabsProps {
  active: BundleTab;
  onChange: (tab: BundleTab) => void;
  snapshotCount?: number;
}

const PRIMARY_TABS: { id: BundleTab; label: string }[] = [
  { id: "preview", label: "Preview" },
  { id: "yaml", label: "Code" },
  { id: "shadow", label: "Shadow" },
];

const OVERFLOW_TABS: { id: BundleTab; label: string; icon: typeof GitCompare }[] = [
  { id: "diff", label: "Diff", icon: GitCompare },
  { id: "history", label: "Snapshots", icon: History },
];

// Icon retained for future overflow surfacing; exported to avoid
// unused-import lints once the shadow tab promotes to overflow.
export const shadowTabIcon = CopyCheck;

export function BundleDetailTabs({ active, onChange, snapshotCount = 0 }: BundleDetailTabsProps) {
  const [dropdownOpen, setDropdownOpen] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);

  // Close dropdown on outside click
  useEffect(() => {
    if (!dropdownOpen) return;
    const handler = (e: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setDropdownOpen(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [dropdownOpen]);

  const isOverflowActive = active === "diff" || active === "history";

  return (
    <div className="flex items-center gap-1 border-b border-border">
      {/* Primary tabs */}
      {PRIMARY_TABS.map((tab) => (
        <button
          type="button"
          key={tab.id}
          className={cn(
            "px-3 py-2 text-xs font-mono transition-colors border-b-2 -mb-px",
            active === tab.id
              ? "border-cordum text-foreground"
              : "border-transparent text-muted-foreground hover:text-foreground",
          )}
          onClick={() => onChange(tab.id)}
        >
          {tab.label}
        </button>
      ))}

      {/* Overflow dropdown */}
      <div className="relative ml-auto" ref={dropdownRef}>
        <button
          type="button"
          className={cn(
            "flex items-center gap-1 px-2 py-2 text-xs font-mono transition-colors border-b-2 -mb-px rounded-t",
            isOverflowActive
              ? "border-cordum text-foreground"
              : "border-transparent text-muted-foreground hover:text-foreground",
          )}
          onClick={() => setDropdownOpen(!dropdownOpen)}
          aria-label="More tabs"
          aria-expanded={dropdownOpen}
        >
          {isOverflowActive && (
            <span className="text-xs">{active === "diff" ? "Diff" : "Snapshots"}</span>
          )}
          <MoreHorizontal className="w-4 h-4" />
        </button>

        {dropdownOpen && (
          <div className="absolute right-0 top-full mt-1 z-20 bg-card border border-border rounded-xl shadow-soft py-1 min-w-[160px]">
            {OVERFLOW_TABS.map((tab) => {
              const Icon = tab.icon;
              const disabled =
                (tab.id === "diff" && snapshotCount < 2) ||
                (tab.id === "history" && snapshotCount === 0);

              return (
                <button
                  key={tab.id}
                  type="button"
                  disabled={disabled}
                  className={cn(
                    "flex items-center gap-2 w-full px-3 py-2 text-xs text-left transition-colors",
                    active === tab.id ? "text-foreground bg-surface-1" : "text-muted-foreground hover:text-foreground hover:bg-surface-1",
                    disabled && "opacity-40 cursor-not-allowed",
                  )}
                  title={disabled ? (tab.id === "diff" ? "No previous version to diff" : "No snapshots yet") : undefined}
                  onClick={() => {
                    if (!disabled) {
                      onChange(tab.id);
                      setDropdownOpen(false);
                    }
                  }}
                >
                  <Icon className="w-3.5 h-3.5" />
                  {tab.label}
                </button>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
