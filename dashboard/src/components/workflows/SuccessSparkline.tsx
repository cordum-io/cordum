import { memo } from "react";
import type { RunStatus } from "../../api/types";
import { cn } from "../../lib/utils";

const COLOR_MAP: Record<string, string> = {
  succeeded: "#1f7a57",
  completed: "#1f7a57",
  failed: "#b83a3a",
  timed_out: "#b83a3a",
  cancelled: "#6b7280",
  running: "#0f7f7a",
  in_progress: "#0f7f7a",
  pending: "#d1d5db",
  waiting: "#c58a1c",
  blocked: "#c58a1c",
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
            height: status === "succeeded" ? "100%" : "75%",
          }}
        />
      ))}
    </div>
  );
});
