import { ShieldOff } from "lucide-react";
import { Card } from "../ui/Card";
import { cn } from "../../lib/utils";
import type { CircuitBreakerState } from "../../hooks/useStatus";

interface FailOpenCounterProps {
  count?: number;
  failMode: "open" | "closed";
  inputCB?: CircuitBreakerState;
}

export function FailOpenCounter({ count, failMode, inputCB }: FailOpenCounterProps) {
  if (count == null) return null;

  const isActivelyBypassing =
    failMode === "open" && inputCB?.state === "OPEN";

  return (
    <Card
      className={cn(
        "transition-colors",
        isActivelyBypassing && "border-danger/40 bg-danger/5",
      )}
    >
      <div className="flex items-center gap-3">
        <ShieldOff
          className={cn(
            "h-5 w-5 shrink-0",
            isActivelyBypassing ? "text-danger" : "text-muted-foreground",
          )}
        />
        <div className="flex-1">
          <p
            className={cn(
              "text-sm font-semibold",
              isActivelyBypassing ? "text-danger" : "text-ink",
            )}
          >
            Jobs passed without safety check
          </p>
          {isActivelyBypassing && (
            <p className="text-xs text-danger">
              Circuit breaker is OPEN and fail-mode is open — jobs are actively
              bypassing safety.
            </p>
          )}
        </div>
        <span
          className={cn(
            "font-mono text-2xl font-bold",
            isActivelyBypassing
              ? "text-danger"
              : count > 0
                ? "text-warning"
                : "text-muted-foreground",
          )}
        >
          {count.toLocaleString()}
        </span>
      </div>
    </Card>
  );
}
