import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { FileText, UserCircle } from "lucide-react";
import { api } from "../lib/api";
import { formatDateTime } from "../lib/format";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Input } from "../components/ui/Input";
import { Badge } from "../components/ui/Badge";
import type { PolicyAuditEntry } from "../types/api";

const actionVariant = (action?: string): "default" | "info" | "warning" | "success" | "danger" => {
  if (!action) {
    return "default";
  }
  const value = action.toLowerCase();
  if (value.includes("rollback") || value.includes("deny")) {
    return "danger";
  }
  if (value.includes("publish") || value.includes("approve")) {
    return "success";
  }
  if (value.includes("edit") || value.includes("update")) {
    return "warning";
  }
  return "info";
};

export function AuditLogPage() {
  const [search, setSearch] = useState("");
  const auditQuery = useQuery({
    queryKey: ["policy", "audit"],
    queryFn: () => api.listPolicyAudit(),
  });

  const entries = useMemo<PolicyAuditEntry[]>(() => auditQuery.data?.items ?? [], [auditQuery.data]);

  const filtered = useMemo(() => {
    if (!search.trim()) {
      return entries;
    }
    const needle = search.toLowerCase();
    return entries.filter((entry) =>
      [entry.action, entry.actor_id, entry.role, entry.message, ...(entry.bundle_ids || [])]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(needle))
    );
  }, [entries, search]);

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Audit Log</CardTitle>
          <div className="text-xs text-muted">Policy governance and publishing activity.</div>
        </CardHeader>
        <Input
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          placeholder="Filter by action, actor, bundle, or message"
          className="max-w-md"
        />
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Recent Entries</CardTitle>
          <div className="text-xs text-muted">{entries.length} total</div>
        </CardHeader>
        {auditQuery.isLoading ? (
          <div className="text-sm text-muted">Loading audit events...</div>
        ) : filtered.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">
            No audit entries match your filters.
          </div>
        ) : (
          <div className="space-y-3">
            {filtered.map((entry) => (
              <div key={entry.id} className="rounded-2xl border border-border bg-white/70 p-4">
                <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
                  <div>
                    <div className="flex items-center gap-2">
                      <FileText className="h-4 w-4 text-accent" />
                      <div className="text-sm font-semibold text-ink">{entry.action}</div>
                      <Badge variant={actionVariant(entry.action)}>{entry.action}</Badge>
                    </div>
                    <div className="mt-2 text-xs text-muted">
                      {entry.message || "No message"}
                    </div>
                    {entry.bundle_ids?.length ? (
                      <div className="mt-2 flex flex-wrap gap-1">
                        {entry.bundle_ids.map((bundle) => (
                          <span key={bundle} className="rounded bg-muted/10 px-2 py-0.5 text-[10px] text-muted">
                            {bundle}
                          </span>
                        ))}
                      </div>
                    ) : null}
                    {entry.snapshot_before || entry.snapshot_after ? (
                      <div className="mt-2 text-[10px] text-muted">
                        {entry.snapshot_before ? `Before ${entry.snapshot_before.slice(0, 8)}` : ""}
                        {entry.snapshot_before && entry.snapshot_after ? " → " : ""}
                        {entry.snapshot_after ? `After ${entry.snapshot_after.slice(0, 8)}` : ""}
                      </div>
                    ) : null}
                  </div>
                  <div className="flex flex-col items-end gap-1 text-xs text-muted">
                    <div className="flex items-center gap-2">
                      <UserCircle className="h-4 w-4" />
                      <span>{entry.actor_id || "system"}</span>
                    </div>
                    <div>{entry.role || "-"}</div>
                    <div>{formatDateTime(entry.created_at)}</div>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}
