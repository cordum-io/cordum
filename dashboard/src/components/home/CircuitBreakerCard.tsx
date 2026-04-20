import { ShieldCheck, ShieldAlert, ShieldOff } from "lucide-react";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { cn } from "../../lib/utils";
import type { CircuitBreakerState } from "../../hooks/useStatus";

// ---------------------------------------------------------------------------
// CircuitBreakerCard — Safety Circuit status for the Command Center
// ---------------------------------------------------------------------------

const stateConfig = {
  CLOSED: {
    label: "Closed",
    tone: "text-success",
    bg: "bg-success/10",
    Icon: ShieldCheck,
  },
  HALF_OPEN: {
    label: "Half-Open",
    tone: "text-warning",
    bg: "bg-warning/10",
    Icon: ShieldAlert,
  },
  OPEN: {
    label: "Open",
    tone: "text-danger",
    bg: "bg-danger/10",
    Icon: ShieldOff,
  },
  unknown: {
    label: "Unknown",
    tone: "text-muted-foreground",
    bg: "bg-surface2",
    Icon: ShieldCheck,
  },
} as const;

function formatCooldown(ms: number): string {
  if (ms <= 0) return "";
  const seconds = Math.ceil(ms / 1000);
  if (seconds < 60) return `Resets in ${seconds}s`;
  const minutes = Math.ceil(seconds / 60);
  return `Resets in ${minutes}m`;
}

function CBSide({
  label,
  cb,
}: {
  label: string;
  cb: CircuitBreakerState;
}) {
  const config = stateConfig[cb.state] ?? stateConfig.unknown;
  const { Icon } = config;
  const cooldown = cb.state === "OPEN" ? formatCooldown(cb.cooldown_remaining_ms) : "";

  return (
    <div className="flex-1 space-y-1.5">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <div className="flex items-center gap-2">
        <span
          className={cn(
            "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-semibold",
            config.bg,
            config.tone,
          )}
        >
          <Icon className="h-3 w-3" />
          {config.label}
        </span>
      </div>
      <p className="text-xs text-muted-foreground">
        Failures: {cb.failures}/{cb.fail_threshold}
      </p>
      {cooldown && (
        <p className={cn("text-xs font-medium", config.tone)}>{cooldown}</p>
      )}
    </div>
  );
}

export function CircuitBreakerCard({
  circuitBreakers,
}: {
  circuitBreakers?: {
    input: CircuitBreakerState;
    output: CircuitBreakerState;
  };
}) {
  // Graceful degradation: hide when HA fields absent
  if (!circuitBreakers) return null;

  const { input, output } = circuitBreakers;
  const anyOpen = input.state === "OPEN" || output.state === "OPEN";
  const anyHalfOpen = input.state === "HALF_OPEN" || output.state === "HALF_OPEN";

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2">
          {anyOpen ? (
            <ShieldOff className="h-4 w-4 text-danger" />
          ) : anyHalfOpen ? (
            <ShieldAlert className="h-4 w-4 text-warning" />
          ) : (
            <ShieldCheck className="h-4 w-4 text-success" />
          )}
          <CardTitle className="text-sm">Safety Circuit</CardTitle>
        </div>
      </CardHeader>
      <div className="flex gap-4">
        <CBSide label="Input Safety" cb={input} />
        <div className="w-px bg-border" />
        <CBSide label="Output Safety" cb={output} />
      </div>
    </Card>
  );
}
