import { Radio } from "lucide-react";
import { cn } from "../../lib/utils";

interface AuditTransportBadgeProps {
  transport?: string;
}

const MODES: Record<string, { label: string; variant: "nats" | "buffer"; tooltip: string }> = {
  nats: {
    label: "NATS-backed (durable)",
    variant: "nats",
    tooltip:
      "Audit events are published via NATS JetStream with at-least-once delivery. Events survive process restarts and are replicated across the cluster.",
  },
  buffer: {
    label: "Buffered (per-process)",
    variant: "buffer",
    tooltip:
      "Audit events are buffered in-memory and flushed periodically. Best-effort delivery — events may be lost on process crash or restart.",
  },
};

export function AuditTransportBadge({ transport }: AuditTransportBadgeProps) {
  if (!transport && transport !== "") return null;

  const key = transport?.toLowerCase() === "nats" ? "nats" : "buffer";
  const mode = MODES[key];

  return (
    <div
      className={cn(
        "inline-flex items-center gap-1.5 rounded-xl border px-2.5 py-1 text-xs",
        key === "nats"
          ? "border-success/30 bg-success/5 text-success"
          : "border-border bg-surface2/30 text-muted-foreground",
      )}
      title={mode.tooltip}
    >
      <Radio className="h-3 w-3" />
      <span>Audit: {mode.label}</span>
    </div>
  );
}

