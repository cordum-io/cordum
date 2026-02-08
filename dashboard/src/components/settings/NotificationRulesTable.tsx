import { useState } from "react";
import { Plus, Pencil, Trash2 } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { ConfirmDialog } from "../ui/ConfirmDialog";
import type { NotificationChannel, NotificationRule } from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatThrottle(ms?: number): string {
  if (!ms) return "\u2014";
  const mins = Math.round(ms / 60_000);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.round(mins / 60);
  return `${hrs}h`;
}

function formatMuteUntil(iso?: string): string {
  if (!iso) return "\u2014";
  const d = new Date(iso);
  if (d.getTime() < Date.now()) return "Expired";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

// ---------------------------------------------------------------------------
// NotificationRulesTable
// ---------------------------------------------------------------------------

interface NotificationRulesTableProps {
  rules: NotificationRule[];
  channels: NotificationChannel[];
  onCreateRule: () => void;
  onEditRule: (rule: NotificationRule) => void;
  onDeleteRule: (id: string) => void;
  onToggleRule: (rule: NotificationRule) => void;
  isDeleting?: boolean;
}

export function NotificationRulesTable({
  rules,
  channels,
  onCreateRule,
  onEditRule,
  onDeleteRule,
  onToggleRule,
  isDeleting,
}: NotificationRulesTableProps) {
  const [deleteId, setDeleteId] = useState<string | null>(null);

  const channelMap = new Map(channels.map((c) => [c.id, c]));

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-ink">Routing Rules</h3>
        <Button variant="outline" size="sm" onClick={onCreateRule}>
          <Plus className="mr-1 h-3 w-3" /> Create Rule
        </Button>
      </div>

      {rules.length === 0 ? (
        <Card>
          <div className="py-8 text-center">
            <p className="text-sm text-muted">
              No routing rules configured.
            </p>
            <p className="mt-1 text-xs text-muted">
              Events will not trigger notifications.
            </p>
          </div>
        </Card>
      ) : (
        <div className="overflow-x-auto rounded-xl border border-border">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-border bg-surface2/50">
                <th className="px-3 py-2 text-left font-medium text-muted">
                  Event Pattern
                </th>
                <th className="px-3 py-2 text-left font-medium text-muted">
                  Channels
                </th>
                <th className="px-3 py-2 text-left font-medium text-muted">
                  Throttle
                </th>
                <th className="px-3 py-2 text-left font-medium text-muted">
                  Mute Until
                </th>
                <th className="px-3 py-2 text-center font-medium text-muted">
                  Enabled
                </th>
                <th className="px-3 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {rules.map((rule) => (
                <tr key={rule.id} className="hover:bg-surface2/30">
                  <td className="px-3 py-2.5">
                    <code className="rounded bg-surface2 px-1.5 py-0.5 font-mono text-xs text-ink">
                      {rule.eventPattern}
                    </code>
                  </td>
                  <td className="px-3 py-2.5">
                    <div className="flex flex-wrap gap-1">
                      {rule.channelIds.map((cid) => {
                        const ch = channelMap.get(cid);
                        return (
                          <Badge key={cid} variant={ch ? "info" : "default"}>
                            {ch?.name ?? cid.slice(0, 8)}
                          </Badge>
                        );
                      })}
                      {rule.channelIds.length === 0 && (
                        <span className="text-muted">{"\u2014"}</span>
                      )}
                    </div>
                  </td>
                  <td className="px-3 py-2.5 text-muted">
                    {formatThrottle(rule.throttleMs)}
                  </td>
                  <td className="px-3 py-2.5 text-muted">
                    {formatMuteUntil(rule.muteUntil)}
                  </td>
                  <td className="px-3 py-2.5 text-center">
                    <input
                      type="checkbox"
                      checked={rule.enabled}
                      onChange={() => onToggleRule(rule)}
                      className="rounded border-border"
                    />
                  </td>
                  <td className="px-3 py-2.5">
                    <div className="flex justify-end gap-1">
                      <button
                        type="button"
                        className="rounded p-1 hover:bg-surface2"
                        onClick={() => onEditRule(rule)}
                      >
                        <Pencil className="h-3 w-3 text-muted" />
                      </button>
                      <button
                        type="button"
                        className="rounded p-1 hover:bg-surface2"
                        onClick={() => setDeleteId(rule.id)}
                      >
                        <Trash2 className="h-3 w-3 text-danger" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <ConfirmDialog
        open={!!deleteId}
        title="Delete Routing Rule"
        message="Delete this routing rule? Events matching this pattern will no longer trigger notifications."
        confirmLabel="Delete"
        confirmVariant="danger"
        isPending={isDeleting}
        onConfirm={() => {
          if (deleteId) {
            onDeleteRule(deleteId);
            setDeleteId(null);
          }
        }}
        onCancel={() => setDeleteId(null)}
      />
    </div>
  );
}
