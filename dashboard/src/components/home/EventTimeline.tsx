import { Badge } from "../ui/Badge";
import { Card } from "../ui/Card";
import { useEventStore } from "../../state/events";
import type { StreamEvent } from "../../api/types";

// ---------------------------------------------------------------------------
// Event type badge variant
// ---------------------------------------------------------------------------

function eventVariant(type: string): "success" | "danger" | "warning" | "info" | "default" | "governance" {
  if (type.includes("deny")) return "governance";
  if (type.includes("fail") || type.includes("error")) return "danger";
  if (type.includes("success") || type.includes("complete") || type.includes("approve")) return "success";
  if (type.includes("warn") || type.includes("throttle") || type.includes("timeout")) return "warning";
  if (type.includes("start") || type.includes("dispatch") || type.includes("submit")) return "info";
  return "default";
}

// ---------------------------------------------------------------------------
// Timestamp
// ---------------------------------------------------------------------------

function formatTime(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function describeEvent(event: StreamEvent): string {
  const p = event.payload;
  if (typeof p.message === "string") return p.message;
  if (typeof p.description === "string") return p.description;
  if (typeof p.jobId === "string") return `Job ${(p.jobId as string).slice(0, 8)}`;
  return event.type;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function EventTimeline() {
  const events = useEventStore((s) => s.events);
  const displayed = events.slice(0, 50);

  return (
    <Card>
      <div className="space-y-3">
        <h2 className="text-sm font-semibold text-ink">Recent Events</h2>

        {displayed.length === 0 ? (
          <p className="py-6 text-center text-xs text-muted-foreground">
            No events yet. Events will appear as activity flows through the system.
          </p>
        ) : (
          <div className="max-h-80 space-y-1.5 overflow-y-auto pr-1">
            {displayed.map((event) => (
              <div
                key={event.id}
                className="flex items-start gap-2 rounded-xl px-2 py-1.5 text-xs hover:bg-surface2/40 transition-colors"
              >
                <span className="shrink-0 font-mono text-muted-foreground w-16">
                  {formatTime(event.timestamp)}
                </span>
                <Badge variant={eventVariant(event.type)} className="shrink-0 text-xs">
                  {event.type}
                </Badge>
                <span className="text-ink truncate">{describeEvent(event)}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </Card>
  );
}

