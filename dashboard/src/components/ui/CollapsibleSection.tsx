import { useState, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { cn } from "../../lib/utils";

interface CollapsibleSectionProps {
  title: string;
  description?: string;
  defaultOpen?: boolean;
  badge?: ReactNode;
  leading?: ReactNode;
  trailing?: ReactNode;
  children: ReactNode;
  className?: string;
  buttonClassName?: string;
  contentClassName?: string;
}

export function CollapsibleSection({
  title,
  description,
  defaultOpen = true,
  badge,
  leading,
  trailing,
  children,
  className,
  buttonClassName,
  contentClassName,
}: CollapsibleSectionProps) {
  const [open, setOpen] = useState(defaultOpen);

  return (
    <div className={cn("", className)}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className={cn(
          "flex w-full items-center justify-between gap-3 rounded-xl px-3 py-2 -mx-3 transition-colors hover:bg-surface-2/50",
          buttonClassName,
        )}
      >
        <span className="flex min-w-0 items-start gap-2">
          {leading && <span className="mt-0.5 shrink-0">{leading}</span>}
          <span className="min-w-0">
            <span className="flex items-center gap-2">
              <span className="font-display text-sm font-semibold text-ink">{title}</span>
              {badge}
            </span>
            {description && (
              <span className="mt-1 block text-xs text-muted-foreground">{description}</span>
            )}
          </span>
        </span>
        <span className="flex items-center gap-2">
          {trailing && <span className="shrink-0">{trailing}</span>}
          <ChevronDown
            className={cn(
              "h-4 w-4 shrink-0 text-muted-foreground transition-transform duration-200",
              open && "rotate-180",
            )}
          />
        </span>
      </button>
      {open && <div className={cn("pt-2", contentClassName)}>{children}</div>}
    </div>
  );
}
