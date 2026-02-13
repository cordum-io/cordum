import { Link } from "react-router-dom";
import { AlertTriangle, CheckCircle } from "lucide-react";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { useDLQ } from "../../hooks/useDLQ";

// ---------------------------------------------------------------------------
// DLQ Summary — compact widget for the Command Center
// ---------------------------------------------------------------------------

export function DLQSummary() {
  const { data, isLoading } = useDLQ({ limit: 5 });
  const entries = data?.items ?? [];

  // Loading skeleton
  if (isLoading) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-sm">Failed Jobs</CardTitle>
        </CardHeader>
        <div className="space-y-2 animate-pulse">
          <div className="h-4 w-1/3 rounded bg-surface2" />
          <div className="h-3 w-full rounded bg-surface2" />
          <div className="h-3 w-4/5 rounded bg-surface2" />
        </div>
      </Card>
    );
  }

  // Empty — no failures
  if (entries.length === 0) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-sm">Failed Jobs</CardTitle>
        </CardHeader>
        <div className="flex items-center gap-2 text-sm text-success">
          <CheckCircle className="h-4 w-4" />
          No failed jobs
        </div>
      </Card>
    );
  }

  // Show recent failures
  const reasons = entries
    .slice(0, 3)
    .map((e) => e.error || e.reason || "Unknown error");

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2">
          <AlertTriangle className="h-4 w-4 text-danger" />
          <CardTitle className="text-sm">Failed Jobs</CardTitle>
        </div>
        <span className="text-lg font-bold text-danger">{entries.length}</span>
      </CardHeader>

      <div className="mt-2 space-y-1.5">
        {reasons.map((reason, i) => (
          <div key={i} className="flex items-start gap-2 text-xs">
            <span className="mt-1 h-1.5 w-1.5 shrink-0 rounded-full bg-danger" />
            <span className="text-muted line-clamp-1">
              {reason.length > 60 ? `${reason.slice(0, 60)}...` : reason}
            </span>
          </div>
        ))}
      </div>

      <Link
        to="/dlq"
        className="mt-3 block text-xs font-medium text-accent hover:underline"
      >
        View all failed jobs &rarr;
      </Link>
    </Card>
  );
}
