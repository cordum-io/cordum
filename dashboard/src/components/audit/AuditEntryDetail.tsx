import { Link } from "react-router-dom";
import { X } from "lucide-react";
import { Badge } from "../ui/Badge";
import { auditResourceLink } from "../../api/transform";
import type { AuditEntry } from "../../api/types";

// ---------------------------------------------------------------------------
// Resource link resolver (uses shared auditResourceLink)
// ---------------------------------------------------------------------------

function resourceLink(
  resourceType: string,
  resourceId: string,
): { to: string; label: string } | null {
  const to = auditResourceLink(resourceType, resourceId);
  if (!to) return null;
  const typeName = resourceType.charAt(0).toUpperCase() + resourceType.slice(1);
  const label = resourceId ? `${typeName} ${resourceId.slice(0, 12)}` : typeName;
  return { to, label };
}

// ---------------------------------------------------------------------------
// Timestamp formatter (ms precision)
// ---------------------------------------------------------------------------

function formatTimestamp(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const date = d.toISOString().slice(0, 10);
  const time = d.toISOString().slice(11, 23);
  return `${date} ${time}`;
}

// ---------------------------------------------------------------------------
// AuditEntryDetail
// ---------------------------------------------------------------------------

interface AuditEntryDetailProps {
  entry: AuditEntry;
  onClose: () => void;
}

export function AuditEntryDetail({ entry, onClose }: AuditEntryDetailProps) {
  const link = resourceLink(entry.resourceType, entry.resourceId);

  return (
    <tr>
      <td colSpan={7} className="bg-surface2/30 px-6 py-4">
        <div className="space-y-4">
          {/* Header */}
          <div className="flex items-start justify-between">
            <div className="space-y-1">
              <div className="flex items-center gap-2">
                <Badge variant="info" className="text-[10px]">
                  {entry.eventType}
                </Badge>
                <span className="font-mono text-xs text-muted-foreground">
                  {formatTimestamp(entry.timestamp)}
                </span>
              </div>
              <p className="text-sm text-ink">{entry.message}</p>
            </div>
            <button
              type="button"
              onClick={onClose}
              className="rounded p-1 text-muted-foreground transition-colors hover:bg-surface2 hover:text-ink"
              aria-label="Close detail"
            >
              <X className="h-4 w-4" />
            </button>
          </div>

          {/* Metadata grid */}
          <div className="grid grid-cols-2 gap-x-6 gap-y-2 text-xs sm:grid-cols-4">
            <div>
              <span className="text-muted-foreground">Actor</span>
              <p className="font-medium text-ink">{entry.actor || "\u2014"}</p>
            </div>
            <div>
              <span className="text-muted-foreground">Action</span>
              <p className="font-medium text-ink">{entry.action || "\u2014"}</p>
            </div>
            <div>
              <span className="text-muted-foreground">Resource Type</span>
              <p className="font-medium text-ink">{entry.resourceType || "\u2014"}</p>
            </div>
            <div>
              <span className="text-muted-foreground">Resource ID</span>
              <p className="font-mono font-medium text-ink" title={entry.resourceId}>
                {entry.resourceId || "\u2014"}
              </p>
            </div>
          </div>

          {/* Linked resource */}
          {link && (
            <div className="text-xs">
              <span className="text-muted-foreground">Navigate to: </span>
              <Link
                to={link.to}
                className="font-medium text-accent underline-offset-2 hover:underline"
              >
                {link.label}
              </Link>
            </div>
          )}

          {/* Full payload */}
          {entry.payload && Object.keys(entry.payload).length > 0 && (
            <div className="space-y-1">
              <span className="text-xs font-semibold text-muted-foreground">Payload</span>
              <pre className="max-h-64 overflow-auto rounded-lg border border-border bg-surface p-3 text-xs text-ink">
                {JSON.stringify(entry.payload, null, 2)}
              </pre>
            </div>
          )}
        </div>
      </td>
    </tr>
  );
}
