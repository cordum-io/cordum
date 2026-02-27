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
    <Card className="flex flex-col gap-2">
      <div className="flex items-center justify-between">
        <p className="text-xs font-medium text-muted">{title}</p>
        {icon}
      </div>
      <div className="text-2xl font-display font-semibold text-ink">{value}</div>
      {detail ? <div className="text-xs text-muted">{detail}</div> : null}
    </Card>
  );
}
