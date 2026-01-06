import type { ReactNode } from "react";
import { Card } from "./ui/Card";

export function MetricCard({
  title,
  value,
  detail,
  icon,
}: {
  title: string;
  value: ReactNode;
  detail?: ReactNode;
  icon?: ReactNode;
}) {
  return (
    <Card className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <p className="text-sm font-semibold uppercase tracking-wide text-muted">{title}</p>
        {icon}
      </div>
      <div className="text-3xl font-display font-semibold text-ink">{value}</div>
      {detail ? <div className="text-xs text-muted">{detail}</div> : null}
    </Card>
  );
}
