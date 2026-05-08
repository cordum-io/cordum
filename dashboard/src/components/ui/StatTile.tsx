import type { ReactNode } from "react";
import { InstrumentCard, InstrumentCardBody } from "@/components/ui/InstrumentCard";
import { MetricValue } from "@/components/ui/MetricValue";
import type { BadgeVariant } from "@/components/ui/StatusBadge";

interface StatTileProps {
  label: string;
  value: string | number;
  /** Single-line helper text rendered as `<p class="text-xs text-muted-foreground">`. Use `children` for richer layouts. */
  helperText?: ReactNode;
  /** Rich helper content rendered without a wrapping element — use this for flex layouts, multi-color spans, mini-bars, or CTA buttons that would be invalid inside a `<p>`. */
  children?: ReactNode;
  icon?: ReactNode;
  accent?: BadgeVariant;
  className?: string;
  size?: "sm" | "md" | "lg";
}

export function StatTile({
  label,
  value,
  helperText,
  children,
  icon,
  accent = "cordum",
  className,
  size = "md",
}: StatTileProps) {
  return (
    <InstrumentCard accent={accent} className={className}>
      <InstrumentCardBody className="p-4">
        <MetricValue label={label} value={value} icon={icon} size={size}>
          {helperText ? (
            <p className="mt-1 text-xs text-muted-foreground">{helperText}</p>
          ) : null}
          {children}
        </MetricValue>
      </InstrumentCardBody>
    </InstrumentCard>
  );
}
