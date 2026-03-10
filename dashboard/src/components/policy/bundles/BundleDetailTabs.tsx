import { cn } from "@/lib/utils";

export type BundleTab = "yaml" | "preview" | "diff" | "history";

const TABS: { id: BundleTab; label: string }[] = [
  { id: "yaml", label: "YAML" },
  { id: "preview", label: "Visual Preview" },
  { id: "diff", label: "Diff" },
  { id: "history", label: "Snapshots" },
];

interface BundleDetailTabsProps {
  active: BundleTab;
  onChange: (tab: BundleTab) => void;
}

export function BundleDetailTabs({ active, onChange }: BundleDetailTabsProps) {
  return (
    <div className="flex gap-1 border-b border-border">
      {TABS.map((tab) => (
        <button
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
    </div>
  );
}
