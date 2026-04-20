import { Database } from "lucide-react";
import { cn } from "../../lib/utils";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatAge(capturedAt: string): { text: string; stale: boolean } {
  const ageMs = Date.now() - new Date(capturedAt).getTime();
  if (Number.isNaN(ageMs) || ageMs < 0) return { text: "just now", stale: false };
  const secs = Math.round(ageMs / 1000);
  const stale = secs > 15;
  if (secs < 60) return { text: `${secs}s ago`, stale };
  const mins = Math.floor(secs / 60);
  return { text: `${mins}m ago`, stale: true };
}

function shortenId(id: string): string {
  // If instance ID is long (e.g. UUID), show first 12 chars.
  return id.length > 16 ? id.slice(0, 12) : id;
}

// ---------------------------------------------------------------------------
// SnapshotWriterBadge
// ---------------------------------------------------------------------------

interface SnapshotWriterBadgeProps {
  snapshotMeta?: {
    writer_id: string;
    captured_at: string;
  };
}

export function SnapshotWriterBadge({ snapshotMeta }: SnapshotWriterBadgeProps) {
  if (!snapshotMeta?.writer_id || !snapshotMeta?.captured_at) return null;

  const { text: ageText, stale } = formatAge(snapshotMeta.captured_at);

  return (
    <div
      className={cn(
        "inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1 text-xs",
        stale
          ? "border-warning/30 bg-warning/5 text-warning"
          : "border-border bg-surface2/30 text-muted-foreground",
      )}
      title={`Snapshot written by ${snapshotMeta.writer_id} at ${new Date(snapshotMeta.captured_at).toLocaleString()}`}
    >
      <Database className="h-3 w-3" />
      <span>
        Snapshot by{" "}
        <span className="font-mono font-medium text-ink">
          {shortenId(snapshotMeta.writer_id)}
        </span>
      </span>
      <span className="opacity-60">&middot;</span>
      <span className={cn("font-mono", stale && "font-semibold")}>{ageText}</span>
    </div>
  );
}
