import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

interface Tab {
  id: string;
  label: string;
  count?: number;
  icon?: ReactNode;
  disabled?: boolean;
}

interface TabsProps {
  tabs: Tab[];
  activeTab: string;
  onChange: (id: string) => void;
  className?: string;
  ariaLabel?: string;
  variant?: "underline" | "segmented";
}

export function Tabs({
  tabs,
  activeTab,
  onChange,
  className,
  ariaLabel = "Tabs",
  variant = "underline",
}: TabsProps) {
  return (
    <div
      role="tablist"
      aria-label={ariaLabel}
      className={cn(
        variant === "segmented"
          ? "flex flex-wrap items-center gap-1 rounded-2xl border border-border bg-surface-1 p-1"
          : "flex items-center gap-1 border-b border-border",
        className,
      )}
    >
      {tabs.map((tab) => (
        <button
          type="button"
          key={tab.id}
          role="tab"
          aria-selected={activeTab === tab.id}
          aria-pressed={activeTab === tab.id}
          aria-label={tab.label}
          tabIndex={activeTab === tab.id ? 0 : -1}
          disabled={tab.disabled}
          onClick={() => !tab.disabled && onChange(tab.id)}
          className={cn(
            "relative inline-flex min-h-9 items-center justify-center rounded-xl px-3 py-2 font-medium transition-colors",
            tab.disabled && "cursor-not-allowed opacity-50",
            variant === "segmented"
              ? activeTab === tab.id
                ? "bg-cordum/10 text-cordum shadow-sm"
                : "text-muted-foreground hover:bg-surface-2/70 hover:text-foreground"
              : activeTab === tab.id
                ? "text-cordum"
                : "text-muted-foreground hover:text-foreground",
          )}
        >
          <span className="flex items-center gap-1.5">
            {tab.icon && <span className="shrink-0">{tab.icon}</span>}
            {tab.label}
            {tab.count !== undefined && (
              <span
                className={cn(
                  "text-xs font-mono px-1.5 py-0.5 rounded-full",
                  activeTab === tab.id
                    ? variant === "segmented"
                      ? "bg-surface-0/80 text-cordum"
                      : "bg-cordum/15 text-cordum"
                    : "bg-surface-2 text-muted-foreground",
                )}
              >
                {tab.count}
              </span>
            )}
          </span>
          {variant === "underline" && activeTab === tab.id && (
            <div className="absolute bottom-0 left-0 right-0 h-[2px] bg-cordum rounded-full" />
          )}
        </button>
      ))}
    </div>
  );
}
