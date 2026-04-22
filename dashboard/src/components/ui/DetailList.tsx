import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface DetailListItem {
  label: ReactNode;
  value: ReactNode;
  mono?: boolean;
  action?: ReactNode;
  align?: "left" | "right";
  valueClassName?: string;
  rowClassName?: string;
}

interface DetailListProps {
  items: DetailListItem[];
  className?: string;
}

export function DetailList({ items, className }: DetailListProps) {
  return (
    <dl className={cn("space-y-0", className)}>
      {items.map((item, index) => (
        <div
          key={typeof item.label === "string" ? item.label : index}
          className={cn(
            "flex items-start justify-between gap-4 border-t border-border/70 py-3 first:border-t-0 first:pt-0 last:pb-0",
            item.rowClassName,
          )}
        >
          <div className="min-w-0 flex-1">
            <dt className="text-sm text-muted-foreground">{item.label}</dt>
            <dd
              className={cn(
                "mt-1 text-sm text-foreground break-all",
                item.align !== "left" && !item.action && "text-right",
                item.mono && "font-mono text-xs",
                item.valueClassName,
              )}
            >
              {item.value}
            </dd>
          </div>
          {item.action ? <div className="shrink-0">{item.action}</div> : null}
        </div>
      ))}
    </dl>
  );
}
