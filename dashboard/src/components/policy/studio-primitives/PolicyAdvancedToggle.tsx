import { cn } from "@/lib/utils";

interface PolicyAdvancedToggleProps {
  open: boolean;
  onToggle: (open: boolean) => void;
  configuredCount?: number;
  label?: string;
}

function nextAdvancedOpenState(open: boolean): boolean {
  return !open;
}

export function PolicyAdvancedToggle({
  open,
  onToggle,
  configuredCount = 0,
  label = "Advanced",
}: PolicyAdvancedToggleProps) {
  return (
    <button
      type="button"
      aria-pressed={open}
      onClick={() => onToggle(nextAdvancedOpenState(open))}
      className={cn(
        "inline-flex items-center gap-2 rounded-md border px-2 py-1 text-xs transition-colors",
        open
          ? "border-cordum/40 bg-cordum/10 text-cordum-foreground"
          : "border-border bg-surface-1 text-muted-foreground hover:text-foreground",
      )}
    >
      <span>{label}</span>
      <span className="rounded bg-surface-2 px-1.5 py-0.5 text-[10px] font-mono text-foreground">
        {configuredCount} configured
      </span>
    </button>
  );
}

export const __policyAdvancedToggleInternal = {
  nextAdvancedOpenState,
};
