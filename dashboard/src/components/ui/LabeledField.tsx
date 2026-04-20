import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

interface LabeledFieldProps {
  label: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
  labelClassName?: string;
  descriptionClassName?: string;
}

export function LabeledField({
  label,
  description,
  action,
  children,
  className,
  labelClassName,
  descriptionClassName,
}: LabeledFieldProps) {
  return (
    <div className={cn("space-y-1.5", className)}>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p
            className={cn(
              "text-xs font-mono uppercase tracking-widest text-muted-foreground",
              labelClassName,
            )}
          >
            {label}
          </p>
          {description && (
            <p
              className={cn(
                "mt-1 text-xs leading-relaxed text-muted-foreground",
                descriptionClassName,
              )}
            >
              {description}
            </p>
          )}
        </div>
        {action && <div className="shrink-0">{action}</div>}
      </div>
      {children}
    </div>
  );
}
