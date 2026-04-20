import { Link } from "react-router-dom";
import { AlertTriangle, CheckCircle } from "lucide-react";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { CardSkeleton } from "../ui/CardSkeleton";
import { CardEmpty } from "../ui/CardEmpty";
import { useDLQ } from "../../hooks/useDLQ";

// ---------------------------------------------------------------------------
// DLQ Summary — compact widget for the Command Center
// ---------------------------------------------------------------------------

export function DLQSummary() {
  const { data, isLoading } = useDLQ({ limit: 5 });
  const entries = data?.items ?? [];

  if (isLoading) {
    return <CardSkeleton rows={3} title="Failed Jobs" />;
  }

  if (entries.length === 0) {
    return <CardEmpty title="Failed Jobs" message="No failed jobs" />;
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
            <span className="text-muted-foreground line-clamp-1">
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
