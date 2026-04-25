import { useState } from "react";
import { Link } from "react-router-dom";
import { X, Copy, Check, ChevronDown, ChevronRight } from "lucide-react";
import { Badge } from "../ui/Badge";
import { cn } from "../../lib/utils";
import type { AuditEntry, AuditCategory, AuditSeverity } from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatTimestampMs(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toISOString().replace("T", " ").replace("Z", " UTC");
}

const categoryLabels: Record<AuditCategory, string> = {
  safety_decision: "Safety Decision",
  human_action: "Human Action",
  system_event: "System Event",
  access_event: "Access Event",
};

const categoryColors: Record<AuditCategory, string> = {
  safety_decision: "bg-primary/10 text-primary",
  human_action: "bg-[var(--color-info)]/10 text-[var(--color-info)]",
  system_event: "bg-muted text-muted-foreground",
  access_event: "bg-[var(--color-warning)]/10 text-[var(--color-warning)]",
};

const severityDot: Record<AuditSeverity, string> = {
  high: "bg-destructive",
  medium: "bg-[var(--color-warning)]",
  low: "bg-transparent",
};

const severityLabels: Record<AuditSeverity, string> = {
  high: "High",
  medium: "Medium",
  low: "Low",
};

// ---------------------------------------------------------------------------
// Copy button
// ---------------------------------------------------------------------------

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      className="ml-1 inline-flex items-center rounded p-0.5 text-muted-foreground hover:text-ink transition-colors"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        } catch {
          // Insecure context or permission denied — leave the copied
          // indicator off so the UI doesn't lie.
        }
      }}
      aria-label="Copy to clipboard"
    >
      {copied ? <Check className="h-3 w-3 text-success" /> : <Copy className="h-3 w-3" />}
    </button>
  );
}

// ---------------------------------------------------------------------------
// Section wrapper
// ---------------------------------------------------------------------------

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="space-y-2">
      <h3 className="text-sm font-semibold text-ink">{title}</h3>
      <hr className="border-border" />
      {children}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Simple line-by-line diff
// ---------------------------------------------------------------------------

interface DiffLine {
  type: "add" | "remove" | "unchanged";
  text: string;
}

function simpleDiff(before: string, after: string): DiffLine[] {
  const bLines = before.split("\n");
  const aLines = after.split("\n");
  const result: DiffLine[] = [];
  const max = Math.max(bLines.length, aLines.length);

  // Simple approach: match by index, flag differences
  for (let i = 0; i < max; i++) {
    const bLine = i < bLines.length ? bLines[i] : undefined;
    const aLine = i < aLines.length ? aLines[i] : undefined;
    if (bLine === aLine) {
      result.push({ type: "unchanged", text: bLine ?? "" });
    } else {
      if (bLine !== undefined) result.push({ type: "remove", text: bLine });
      if (aLine !== undefined) result.push({ type: "add", text: aLine });
    }
  }
  return result;
}

const diffLineClass: Record<DiffLine["type"], string> = {
  add: "bg-[var(--color-success)]/5 text-[var(--color-success)]",
  remove: "bg-destructive/5 text-destructive",
  unchanged: "",
};

const diffPrefix: Record<DiffLine["type"], string> = {
  add: "+ ",
  remove: "- ",
  unchanged: "  ",
};

// ---------------------------------------------------------------------------
// AuditDetailPanel
// ---------------------------------------------------------------------------

interface AuditDetailPanelProps {
  entry: AuditEntry | null;
  onClose: () => void;
}

export function AuditDetailPanel({ entry, onClose }: AuditDetailPanelProps) {
  const [integrityOpen, setIntegrityOpen] = useState(false);

  if (!entry) return null;

  const category = entry.category ?? "system_event";
  const severity = entry.severity ?? "low";

  const beforeStr = entry.snapshotBefore ? JSON.stringify(entry.snapshotBefore, null, 2) : null;
  const afterStr = entry.snapshotAfter ? JSON.stringify(entry.snapshotAfter, null, 2) : null;
  const diffLines = beforeStr && afterStr ? simpleDiff(beforeStr, afterStr) : null;

  return (
    <>
      {/* Backdrop */}
      <div className="fixed inset-0 z-40 bg-black/30" onClick={onClose} />

      {/* Panel */}
      <div
        className={cn(
          "fixed right-0 top-0 z-50 flex h-full flex-col",
          "w-full md:w-[65%] border-l border-border bg-surface shadow-2xl",
          "translate-x-0 transition-transform duration-200 ease-out",
        )}
      >
        {/* Header */}
        <div className="border-b border-border px-6 py-4">
          <div className="flex items-start justify-between">
            <div className="space-y-1.5 min-w-0 flex-1">
              {/* Event ID */}
              <div className="flex items-center gap-1">
                <span className="font-mono text-xs text-muted-foreground" title={entry.id}>
                  {entry.id}
                </span>
                <CopyButton text={entry.id} />
              </div>
              {/* Timestamp */}
              <p className="font-mono text-xs text-muted-foreground">
                {formatTimestampMs(entry.timestamp)}
              </p>
              {/* Badges */}
              <div className="flex flex-wrap items-center gap-2">
                <span className={cn("rounded-full px-2 py-0.5 text-xs font-medium", categoryColors[category])}>
                  {categoryLabels[category]}
                </span>
                <Badge variant="info">{entry.action || entry.eventType}</Badge>
                {severity !== "low" && (
                  <div className="flex items-center gap-1">
                    <span className={cn("h-2 w-2 rounded-full", severityDot[severity])} />
                    <span className="text-xs text-muted-foreground">{severityLabels[severity]}</span>
                  </div>
                )}
              </div>
            </div>
            <button
              type="button"
              onClick={onClose}
              className="ml-3 rounded p-1.5 text-muted-foreground hover:bg-surface2 hover:text-ink transition-colors"
              aria-label="Close panel"
            >
              <X className="h-5 w-5" />
            </button>
          </div>
        </div>

        {/* Scrollable body */}
        <div className="flex-1 overflow-y-auto px-6 py-5 space-y-6">
          {/* Actor block */}
          <Section title="Actor">
            {entry.actorInfo ? (
              <div className="grid grid-cols-2 gap-x-6 gap-y-2 text-xs">
                <div>
                  <span className="text-muted-foreground">Identity</span>
                  <p className="font-medium text-ink">{entry.actorInfo.name || entry.actorInfo.id}</p>
                </div>
                <div>
                  <span className="text-muted-foreground">Type</span>
                  <p className="font-medium text-ink capitalize">{entry.actorInfo.type.replace("_", " ")}</p>
                </div>
                {entry.actorInfo.role && (
                  <div>
                    <span className="text-muted-foreground">Role</span>
                    <p className="font-medium text-ink">{entry.actorInfo.role}</p>
                  </div>
                )}
              </div>
            ) : (
              <p className="text-xs text-ink">{entry.actor}</p>
            )}
          </Section>

          {/* Resource block */}
          <Section title="Resource">
            <div className="space-y-1 text-xs">
              <p>
                <span className="text-muted-foreground">Type: </span>
                <span className="font-medium text-ink">{entry.resourceType}</span>
              </p>
              {entry.resourceId && (
                <p>
                  <span className="text-muted-foreground">ID: </span>
                  <span className="font-mono text-ink">{entry.resourceId}</span>
                </p>
              )}
              {entry.resourceInfo?.link && (
                <Link
                  to={entry.resourceInfo.link}
                  className="inline-block mt-1 text-accent hover:underline"
                >
                  View {entry.resourceType} &rarr;
                </Link>
              )}
            </div>
          </Section>

          {/* Action block */}
          <Section title="Action">
            <div className="space-y-2 text-xs">
              <p>
                <span className="font-semibold text-ink">{entry.action}</span>
                {entry.message && (
                  <span className="text-muted-foreground"> — {entry.message}</span>
                )}
              </p>
              {entry.bundleIds && entry.bundleIds.length > 0 && (
                <p>
                  <span className="text-muted-foreground">Bundles: </span>
                  <span className="text-ink">{entry.bundleIds.join(", ")}</span>
                </p>
              )}
            </div>
          </Section>

          {/* Before/After diff block */}
          {(beforeStr || afterStr) && (
            <Section title="Before / After">
              {diffLines ? (
                <pre className="max-h-96 overflow-auto rounded-xl border border-border p-3 text-xs font-mono">
                  {diffLines.map((line, i) => (
                    <div key={i} className={cn("px-1", diffLineClass[line.type])}>
                      {diffPrefix[line.type]}{line.text}
                    </div>
                  ))}
                </pre>
              ) : beforeStr ? (
                <div>
                  <p className="mb-1 text-xs text-muted-foreground">State Before</p>
                  <pre className="max-h-64 overflow-auto rounded-xl border border-border bg-surface2/50 p-3 text-xs font-mono text-ink">
                    {beforeStr}
                  </pre>
                </div>
              ) : afterStr ? (
                <div>
                  <p className="mb-1 text-xs text-muted-foreground">State After</p>
                  <pre className="max-h-64 overflow-auto rounded-xl border border-border bg-surface2/50 p-3 text-xs font-mono text-ink">
                    {afterStr}
                  </pre>
                </div>
              ) : null}
            </Section>
          )}

          {/* Context block */}
          <Section title="Related Events">
            {entry.resourceId ? (
              <div className="text-xs">
                <p className="text-muted-foreground">
                  This event involves {entry.resourceType}{" "}
                  <span className="font-mono text-ink">{entry.resourceId.slice(0, 16)}</span>.
                </p>
                <Link
                  to={`/audit?resource=${encodeURIComponent(`${entry.resourceType}:${entry.resourceId}`)}&view=correlation`}
                  className="mt-1 inline-block text-accent hover:underline"
                >
                  View all related events &rarr;
                </Link>
              </div>
            ) : (
              <p className="text-xs text-muted-foreground">No resource association.</p>
            )}
          </Section>

          {/* Integrity block (collapsed) */}
          <div>
            <button
              type="button"
              className="flex items-center gap-1 text-xs text-muted-foreground hover:text-ink transition-colors"
              onClick={() => setIntegrityOpen((v) => !v)}
            >
              {integrityOpen ? (
                <ChevronDown className="h-3 w-3" />
              ) : (
                <ChevronRight className="h-3 w-3" />
              )}
              Audit Integrity
            </button>
            {integrityOpen && (
              <div className="mt-2 space-y-1 text-xs text-muted-foreground pl-4">
                <p>
                  <span className="text-muted-foreground">Event ID: </span>
                  <span className="font-mono text-ink">{entry.id}</span>
                </p>
                <p>
                  <span className="text-muted-foreground">Server timestamp: </span>
                  <span className="font-mono text-ink">{entry.timestamp}</span>
                </p>
                <p>
                  <span className="text-muted-foreground">ID hash: </span>
                  <span className="font-mono text-ink">{entry.id.slice(-12)}</span>
                </p>
              </div>
            )}
          </div>
        </div>
      </div>
    </>
  );
}

