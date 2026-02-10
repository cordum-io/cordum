import { memo } from "react";
import type { RunStatus } from "../../api/types";
import { cn } from "../../lib/utils";

const COLOR_MAP: Record<string, string> = {
  succeeded: "#22c55e",
  completed: "#22c55e",
  failed: "#ef4444",
  timed_out: "#ef4444",
  cancelled: "#6b7280",
  running: "#3b82f6",
  in_progress: "#3b82f6",
  pending: "#d1d5db",
  waiting: "#f59e0b",
  blocked: "#f59e0b",
  queued: "#d1d5db",
};

export interface SuccessSparklineProps {
  data: RunStatus[];
  className?: string;
}

export const SuccessSparkline = memo(function SuccessSparkline({
  data,
  className,
}: SuccessSparklineProps) {
  if (data.length === 0) return null;

  return (
    <div className={cn("flex items-end gap-px h-4", className)}>
      {data.map((status, i) => (
        <div
          key={i}
          className="w-0.5 rounded-sm"
          style={{
            backgroundColor: COLOR_MAP[status] ?? "#6b7280",
            height: status === "succeeded" || status === "completed" ? "100%" : "75%",
          }}
        />
      ))}
    </div>
  );
});
