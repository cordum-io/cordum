import { cn } from "../../lib/utils";
import { ProgressBar } from "../ProgressBar";
import type { RetryAttempt } from "../../api/types";

function formatTimestamp(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function durationBetween(a: string, b: string): string {
  const ms = new Date(b).getTime() - new Date(a).getTime();
  if (ms < 0 || Number.isNaN(ms)) return "--";
  if (ms < 1_000) return `${ms}ms`;
  const secs = Math.floor(ms / 1_000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  const remSecs = secs % 60;
  return `${mins}m ${remSecs}s`;
}

export function RetryAttemptsPanel({
  attempts,
  retryCount,
  maxRetries,
}: {
  attempts?: RetryAttempt[];
  retryCount: number;
  maxRetries: number;
}) {
  const pct = maxRetries > 0 ? Math.round((retryCount / maxRetries) * 100) : 0;
  const variant = pct >= 100 ? "danger" : pct >= 60 ? "warning" : "default";

  return (
    <tr>
      <td colSpan={6} className="bg-surface2/30 px-8 py-5">
        <div className="space-y-4">
          {/* Header: attempt count & progress */}
          <div className="flex items-center gap-4">
            <span className="text-xs font-semibold uppercase tracking-wider text-muted">
              Retry Attempts
            </span>
            <span className="text-xs text-muted">
              {retryCount} / {maxRetries}
            </span>
            <div className="w-32">
              <ProgressBar value={pct} variant={variant} />
            </div>
          </div>

          {/* Timeline */}
          {(!attempts || attempts.length === 0) ? (
            <p className="text-xs text-muted">No retry attempts recorded.</p>
          ) : (
            <div className="relative space-y-0">
              {attempts.map((attempt, idx) => {
                const duration =
                  idx < attempts.length - 1
                    ? durationBetween(attempt.attemptedAt, attempts[idx + 1].attemptedAt)
                    : null;

                return (
                  <div key={idx} className="relative flex gap-4 pb-4 last:pb-0">
                    {/* Timeline line + dot */}
                    <div className="flex flex-col items-center">
                      <div
                        className={cn(
                          "flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-[10px] font-bold",
                          idx === attempts.length - 1
                            ? "bg-danger/20 text-danger"
                            : "bg-surface text-muted border border-border",
                        )}
                      >
                        {idx + 1}
                      </div>
                      {idx < attempts.length - 1 && (
                        <div className="w-px flex-1 bg-border" />
                      )}
                    </div>

                    {/* Content */}
                    <div className="flex-1 space-y-1 min-w-0">
                      <div className="flex items-baseline gap-3">
                        <span className="text-xs font-medium text-ink">
                          {formatTimestamp(attempt.attemptedAt)}
                        </span>
                        {duration && (
                          <span className="text-[10px] text-muted">
                            +{duration}
                          </span>
                        )}
                      </div>
                      <pre className="whitespace-pre-wrap break-all rounded-lg bg-surface p-2.5 font-mono text-xs text-danger">
                        {attempt.error}
                      </pre>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </td>
    </tr>
  );
}
