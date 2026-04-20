import type { ReactNode } from "react";
import { InstrumentCard, InstrumentCardBody } from "@/components/ui/InstrumentCard";
import { MetricValue } from "@/components/ui/MetricValue";
import type { BadgeVariant } from "@/components/ui/StatusBadge";

interface StatTileProps {
  label: string;
  value: string | number;
  helperText?: ReactNode;
  icon?: ReactNode;
  accent?: BadgeVariant;
  className?: string;
  size?: "sm" | "md" | "lg";
}

export function StatTile({
  label,
  value,
  helperText,
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
        </MetricValue>
      </InstrumentCardBody>
    </InstrumentCard>
  );
}
