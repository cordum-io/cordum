import { useMemo, useState } from "react";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { AlertTriangle, RotateCcw, Trash2 } from "lucide-react";
import { api } from "../lib/api";
import { formatRelative } from "../lib/format";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Badge } from "../components/ui/Badge";
import type { DLQEntry } from "../types/api";

const statusVariant = (status?: string): "default" | "warning" | "danger" | "success" | "info" => {
  if (!status) {
    return "default";
  }
  const normalized = status.toLowerCase();
  if (["failed", "denied", "error"].includes(normalized)) {
    return "danger";
  }
  if (["retrying", "pending"].includes(normalized)) {
    return "warning";
  }
  if (["resolved", "succeeded"].includes(normalized)) {
    return "success";
  }
  return "info";
};

export function DLQPage() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");

  const dlqQuery = useInfiniteQuery({
    queryKey: ["dlq", "page"],
    queryFn: ({ pageParam }) => api.listDLQPage(100, pageParam as number | undefined),
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    initialPageParam: undefined as number | undefined,
  });

  const retryMutation = useMutation({
    mutationFn: (jobId: string) => api.retryDLQ(jobId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["dlq"] }),
  });

  const deleteMutation = useMutation({
    mutationFn: (jobId: string) => api.deleteDLQ(jobId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["dlq"] }),
  });

  const entries = useMemo<DLQEntry[]>(
    () => dlqQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [dlqQuery.data]
  );

  const filtered = useMemo(() => {
    if (!search.trim()) {
      return entries;
    }
    const needle = search.toLowerCase();
    return entries.filter((entry) =>
      [entry.job_id, entry.topic, entry.reason, entry.reason_code, entry.status]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(needle))
    );
  }, [entries, search]);

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Dead Letter Queue</CardTitle>
          <div className="text-xs text-muted">Investigate failures and replay jobs safely.</div>
        </CardHeader>
        <div className="flex flex-wrap items-center gap-3">
          <Input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Filter by job id, topic, or reason"
            className="max-w-md"
          />
          <Button
            variant="outline"
            size="sm"
            type="button"
            onClick={() => queryClient.invalidateQueries({ queryKey: ["dlq"] })}
          >
            Refresh
          </Button>
        </div>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>DLQ Entries</CardTitle>
          <div className="text-xs text-muted">{entries.length} total</div>
        </CardHeader>
        {dlqQuery.isLoading ? (
          <div className="text-sm text-muted">Loading DLQ entries...</div>
        ) : filtered.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
            No DLQ items match your filters.
          </div>
        ) : (
          <div className="space-y-3">
            {filtered.map((entry) => (
              <div key={entry.job_id} className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
                  <div>
                    <div className="flex items-center gap-2">
                      <AlertTriangle className="h-4 w-4 text-warning" />
                      <div className="text-sm font-semibold text-ink">Job {entry.job_id.slice(0, 8)}</div>
                      {entry.status ? <Badge variant={statusVariant(entry.status)}>{entry.status}</Badge> : null}
                    </div>
                    <div className="mt-2 text-xs text-muted">{entry.topic || "Unknown topic"}</div>
                    <div className="mt-1 text-xs text-muted">
                      {entry.reason_code ? `${entry.reason_code} · ` : ""}
                      {entry.reason || "No reason provided"}
                    </div>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <div className="text-[11px] text-muted">{formatRelative(entry.created_at)}</div>
                    <Link to={`/jobs/${entry.job_id}`}>
                      <Button variant="outline" size="sm" type="button">
                        View Job
                      </Button>
                    </Link>
                    <Button
                      variant="primary"
                      size="sm"
                      type="button"
                      onClick={() => retryMutation.mutate(entry.job_id)}
                      disabled={retryMutation.isPending}
                    >
                      <RotateCcw className="h-3 w-3" />
                      Replay
                    </Button>
                    <Button
                      variant="danger"
                      size="sm"
                      type="button"
                      onClick={() => deleteMutation.mutate(entry.job_id)}
                      disabled={deleteMutation.isPending}
                    >
                      <Trash2 className="h-3 w-3" />
                      Delete
                    </Button>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
        {dlqQuery.hasNextPage ? (
          <div className="mt-4">
            <Button
              variant="outline"
              size="sm"
              type="button"
              onClick={() => dlqQuery.fetchNextPage()}
              disabled={dlqQuery.isFetchingNextPage}
            >
              {dlqQuery.isFetchingNextPage ? "Loading..." : "Load more"}
            </Button>
          </div>
        ) : null}
      </Card>
    </div>
  );
}
