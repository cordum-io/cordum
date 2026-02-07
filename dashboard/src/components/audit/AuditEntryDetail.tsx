import { Link } from "react-router-dom";
import { X } from "lucide-react";
import { Badge } from "../ui/Badge";
import type { AuditEntry } from "../../api/types";

// ---------------------------------------------------------------------------
// Resource link resolver
// ---------------------------------------------------------------------------

function resourceLink(
  resourceType: string,
  resourceId: string,
): { to: string; label: string } | null {
  switch (resourceType.toLowerCase()) {
    case "job":
      return { to: `/jobs/${resourceId}`, label: `Job ${resourceId.slice(0, 12)}` };
    case "workflow":
      return { to: `/workflows/${resourceId}`, label: `Workflow ${resourceId.slice(0, 12)}` };
    case "run":
      return { to: `/workflows`, label: `Run ${resourceId.slice(0, 12)}` };
    case "policy":
      return { to: `/policies`, label: "Policies" };
    case "user":
      return { to: `/settings`, label: `User ${resourceId.slice(0, 12)}` };
    case "pack":
      return { to: `/packs`, label: `Pack ${resourceId.slice(0, 12)}` };
    case "approval":
      return { to: `/approvals`, label: `Approval ${resourceId.slice(0, 12)}` };
    default:
      return null;
  }
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
                <span className="font-mono text-xs text-muted">
                  {formatTimestamp(entry.timestamp)}
                </span>
              </div>
              <p className="text-sm text-ink">{entry.message}</p>
            </div>
            <button
              type="button"
              onClick={onClose}
              className="rounded p-1 text-muted transition-colors hover:bg-surface2 hover:text-ink"
              aria-label="Close detail"
            >
              <X className="h-4 w-4" />
            </button>
          </div>

          {/* Metadata grid */}
          <div className="grid grid-cols-2 gap-x-6 gap-y-2 text-xs sm:grid-cols-4">
            <div>
              <span className="text-muted">Actor</span>
              <p className="font-medium text-ink">{entry.actor || "\u2014"}</p>
            </div>
            <div>
              <span className="text-muted">Action</span>
              <p className="font-medium text-ink">{entry.action || "\u2014"}</p>
            </div>
            <div>
              <span className="text-muted">Resource Type</span>
              <p className="font-medium text-ink">{entry.resourceType || "\u2014"}</p>
            </div>
            <div>
              <span className="text-muted">Resource ID</span>
              <p className="font-mono font-medium text-ink" title={entry.resourceId}>
                {entry.resourceId || "\u2014"}
              </p>
            </div>
          </div>

          {/* Linked resource */}
          {link && (
            <div className="text-xs">
              <span className="text-muted">Navigate to: </span>
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
              <span className="text-xs font-semibold text-muted">Payload</span>
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
