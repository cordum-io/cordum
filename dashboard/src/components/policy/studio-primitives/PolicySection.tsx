import { ChevronDown, ChevronRight } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { cn } from "@/lib/utils";

interface PolicySectionProps {
  title: string;
  description?: string;
  children: ReactNode;
  className?: string;
  rightSlot?: ReactNode;
  defaultOpen?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}

function toggleSectionOpen(current: boolean): boolean {
  return !current;
}

export function PolicySection({
  title,
  description,
  children,
  className,
  rightSlot,
  defaultOpen = true,
  open,
  onOpenChange,
}: PolicySectionProps) {
  const [internalOpen, setInternalOpen] = useState(defaultOpen);
  const isOpen = typeof open === "boolean" ? open : internalOpen;
  const sectionId = useId();

  const setOpen = (next: boolean) => {
    if (typeof open !== "boolean") {
      setInternalOpen(next);
    }
    onOpenChange?.(next);
  };

  return (
    <section className={cn("rounded-md border border-border bg-surface-0", className)}>
      <div className="flex items-start justify-between gap-2 border-b border-border px-3 py-2">
        <button
          type="button"
          onClick={() => setOpen(toggleSectionOpen(isOpen))}
          aria-expanded={isOpen}
          aria-controls={sectionId}
          className="flex min-w-0 items-start gap-2 text-left text-xs text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-cordum"
        >
          {isOpen ? (
            <ChevronDown className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronRight className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          )}
          <span>
            <span className="font-semibold">{title}</span>
            {description && <span className="mt-0.5 block text-[11px] text-muted-foreground">{description}</span>}
          </span>
        </button>
        {rightSlot}
      </div>
      {isOpen && (
        <div id={sectionId} className="space-y-3 px-3 py-3">
          {children}
        </div>
      )}
    </section>
  );
}

export const __policySectionInternal = {
  toggleSectionOpen,
};
