import { useEffect, useState, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Timer } from "lucide-react";
import { Badge } from "../ui/Badge";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatCountdown(ms: number): string {
  if (ms <= 0) return "now";
  const totalSecs = Math.ceil(ms / 1000);
  if (totalSecs < 60) return `${totalSecs}s`;
  const mins = Math.floor(totalSecs / 60);
  const secs = totalSecs % 60;
  if (mins < 60) return `${mins}m ${secs}s`;
  const hours = Math.floor(mins / 60);
  const remMins = mins % 60;
  return `${hours}h ${remMins}m`;
}

function formatAbsoluteTime(iso: string): string {
  try {
    return new Date(iso).toLocaleTimeString();
  } catch {
    return iso;
  }
}

// ---------------------------------------------------------------------------
// DelayTimerBadge
// ---------------------------------------------------------------------------

export interface DelayTimerInfo {
  workflow_id: string;
  run_id: string;
  fires_at: string;
  remaining_ms: number;
}

interface DelayTimerBadgeProps {
  timer: DelayTimerInfo;
  runId: string;
}

export function DelayTimerBadge({ timer, runId }: DelayTimerBadgeProps) {
  const queryClient = useQueryClient();
  const queryClientRef = useRef(queryClient);
  queryClientRef.current = queryClient;

  const [remainingMs, setRemainingMs] = useState(() => {
    // Compute from fires_at for accuracy (remaining_ms may be stale from fetch).
    const delta = new Date(timer.fires_at).getTime() - Date.now();
    return Math.max(0, delta);
  });

  useEffect(() => {
    const interval = setInterval(() => {
      const delta = new Date(timer.fires_at).getTime() - Date.now();
      if (delta <= 0) {
        setRemainingMs(0);
        clearInterval(interval);
        // Timer expired — refresh run detail to pick up resumed state.
        queryClientRef.current.invalidateQueries({
          queryKey: ["workflow-runs", runId],
        });
      } else {
        setRemainingMs(delta);
      }
    }, 1000);

    return () => clearInterval(interval);
  }, [timer.fires_at, runId]);

  if (remainingMs <= 0) return null;

  return (
    <Badge variant="info" className="gap-1">
      <Timer className="h-3 w-3" />
      Resumes in {formatCountdown(remainingMs)}
      <span className="text-[10px] opacity-70">
        ({formatAbsoluteTime(timer.fires_at)})
      </span>
    </Badge>
  );
}
