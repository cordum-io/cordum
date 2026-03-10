import { Clock, RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { usePolicySnapshots } from "@/hooks/usePolicies";

interface BundleSnapshotHistoryProps {
  canRollback: boolean;
  onRollback: (snapshotId: string) => void;
}

export function BundleSnapshotHistory({ canRollback, onRollback }: BundleSnapshotHistoryProps) {
  const { data, isLoading, isError } = usePolicySnapshots();
  const snapshots = data?.items ?? [];

  if (isLoading) {
    return (
      <div className="space-y-3">
        <SkeletonCard />
        <SkeletonCard />
      </div>
    );
  }

  if (isError) {
    return (
      <div className="rounded-2xl border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
        Failed to load snapshot history.
      </div>
    );
  }

  if (snapshots.length === 0) {
    return (
      <EmptyState
        icon={<Clock className="h-6 w-6" />}
        title="No snapshots"
        description="No policy snapshots have been captured yet. Publish changes to create automatic snapshots."
      />
    );
  }

  return (
    <div className="space-y-3">
      <p className="text-[10px] font-mono uppercase tracking-wider text-muted-foreground">
        {snapshots.length} snapshot{snapshots.length !== 1 ? "s" : ""}
      </p>
      <div className="divide-y divide-border rounded-lg border border-border">
        {snapshots.map((snapshot) => (
          <div key={snapshot.id} className="flex items-center gap-3 px-4 py-3">
            <Clock className="h-4 w-4 shrink-0 text-muted-foreground" />
            <div className="min-w-0 flex-1">
              <p className="text-xs font-mono text-foreground truncate">{snapshot.id}</p>
              <p className="text-[11px] text-muted-foreground">
                {snapshot.createdAt
                  ? new Date(snapshot.createdAt).toLocaleString()
                  : "Unknown date"}
                {snapshot.note ? ` — ${snapshot.note}` : ""}
                {snapshot.createdBy ? ` by ${snapshot.createdBy}` : ""}
              </p>
            </div>
            {canRollback && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => onRollback(snapshot.id)}
              >
                <RotateCcw className="mr-1 h-3 w-3" />
                Rollback
              </Button>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
