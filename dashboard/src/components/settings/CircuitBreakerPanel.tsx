import { ShieldCheck, ShieldAlert, ShieldOff } from "lucide-react";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { Badge } from "../ui/Badge";
import { cn } from "../../lib/utils";
import type { CircuitBreakerState } from "../../hooks/useStatus";

// ---------------------------------------------------------------------------
// State config
// ---------------------------------------------------------------------------

const STATE_CONFIG = {
  CLOSED: {
    label: "Closed",
    variant: "success" as const,
    Icon: ShieldCheck,
    description: "All checks active. Requests evaluated normally.",
  },
  HALF_OPEN: {
    label: "Half-Open",
    variant: "warning" as const,
    Icon: ShieldAlert,
    description: "Probing recovery. Next failure re-opens the circuit.",
  },
  OPEN: {
    label: "Open",
    variant: "danger" as const,
    Icon: ShieldOff,
    description: "Circuit tripped. Using fallback mode.",
  },
  unknown: {
    label: "Unknown",
    variant: "default" as const,
    Icon: ShieldCheck,
    description: "State unavailable.",
  },
} as const;

// ---------------------------------------------------------------------------
// Mini state diagram
// ---------------------------------------------------------------------------

const DIAGRAM_STATES = ["CLOSED", "OPEN", "HALF_OPEN"] as const;
const DIAGRAM_LABELS: Record<string, string> = {
  CLOSED: "Closed",
  OPEN: "Open",
  HALF_OPEN: "Half-Open",
};

function StateDiagram({ current }: { current: string }) {
  return (
    <div className="flex items-center gap-1">
      {DIAGRAM_STATES.map((s, i) => {
        const active = s === current;
        return (
          <div key={s} className="flex items-center gap-1">
            <span
              className={cn(
                "rounded-full px-2 py-0.5 text-[10px] font-medium transition-colors",
                active
                  ? s === "CLOSED"
                    ? "bg-success/15 text-success ring-1 ring-success/30"
                    : s === "OPEN"
                      ? "bg-danger/15 text-danger ring-1 ring-danger/30"
                      : "bg-warning/15 text-warning ring-1 ring-warning/30"
                  : "bg-surface2 text-muted",
              )}
            >
              {DIAGRAM_LABELS[s]}
            </span>
            {i < DIAGRAM_STATES.length - 1 && (
              <span className="text-[10px] text-muted">&rarr;</span>
            )}
          </div>
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatCooldown(ms: number): string {
  if (ms <= 0) return "Expired";
  const secs = Math.ceil(ms / 1000);
  if (secs < 60) return `${secs}s remaining`;
  const mins = Math.ceil(secs / 60);
  return `${mins}m remaining`;
}

// ---------------------------------------------------------------------------
// Single CB section
// ---------------------------------------------------------------------------

function CBSection({
  title,
  fallbackMode,
  cb,
}: {
  title: string;
  fallbackMode: string;
  cb: CircuitBreakerState;
}) {
  const config = STATE_CONFIG[cb.state] ?? STATE_CONFIG.unknown;
  const { Icon } = config;

  return (
    <div className="flex-1 space-y-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Icon className={cn("h-4 w-4", `text-${config.variant}`)} />
          <span className="text-sm font-semibold text-ink">{title}</span>
        </div>
        <Badge variant={config.variant}>{config.label}</Badge>
      </div>

      <p className="text-xs text-muted">{config.description}</p>

      <StateDiagram current={cb.state} />

      <div className="space-y-1.5 text-xs">
        <div className="flex justify-between">
          <span className="text-muted">Consecutive failures</span>
          <span className="font-mono text-ink">
            {cb.failures} / {cb.fail_threshold}
          </span>
        </div>
        <div className="flex justify-between">
          <span className="text-muted">Fallback mode</span>
          <span className="text-ink">{fallbackMode}</span>
        </div>
        {cb.state === "OPEN" && cb.cooldown_remaining_ms > 0 && (
          <div className="flex justify-between">
            <span className="text-muted">Cooldown</span>
            <span className="font-mono text-danger">
              {formatCooldown(cb.cooldown_remaining_ms)}
            </span>
          </div>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// CircuitBreakerPanel (exported)
// ---------------------------------------------------------------------------

export function CircuitBreakerPanel({
  circuitBreakers,
}: {
  circuitBreakers?: {
    input: CircuitBreakerState;
    output: CircuitBreakerState;
  };
}) {
  if (!circuitBreakers) return null;

  const { input, output } = circuitBreakers;
  const anyOpen = input.state === "OPEN" || output.state === "OPEN";

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2">
          {anyOpen ? (
            <ShieldOff className="h-4 w-4 text-danger" />
          ) : (
            <ShieldCheck className="h-4 w-4 text-success" />
          )}
          <CardTitle className="text-sm">Circuit Breakers</CardTitle>
        </div>
      </CardHeader>

      <div className="flex flex-col gap-6 sm:flex-row">
        <CBSection
          title="Input Safety"
          fallbackMode="deny (fail-closed)"
          cb={input}
        />
        <div className="hidden sm:block w-px bg-border" />
        <div className="sm:hidden h-px bg-border" />
        <CBSection
          title="Output Safety"
          fallbackMode="allow (fail-open)"
          cb={output}
        />
      </div>
    </Card>
  );
}
