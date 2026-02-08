import { useState } from "react";
import { Mail, Hash, Bell, Globe, Pencil, Play, Trash2 } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { ConfirmDialog } from "../ui/ConfirmDialog";
import type { NotificationChannel, NotificationChannelType } from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const CHANNEL_ICONS: Record<NotificationChannelType, typeof Mail> = {
  email: Mail,
  slack: Hash,
  pagerduty: Bell,
  webhook: Globe,
};

const CHANNEL_LABELS: Record<NotificationChannelType, string> = {
  email: "Email",
  slack: "Slack",
  pagerduty: "PagerDuty",
  webhook: "Webhook",
};

function statusBadge(ch: NotificationChannel) {
  if (ch.error) return <Badge variant="danger">Error</Badge>;
  if (!ch.enabled) return <Badge variant="default">Disabled</Badge>;
  return <Badge variant="success">Active</Badge>;
}

function timeAgo(iso?: string): string {
  if (!iso) return "Never";
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1_000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

// ---------------------------------------------------------------------------
// NotificationChannelCard
// ---------------------------------------------------------------------------

interface NotificationChannelCardProps {
  channel: NotificationChannel;
  onEdit: (channel: NotificationChannel) => void;
  onTest: (channel: NotificationChannel) => void;
  onDelete: (id: string) => void;
  isDeleting?: boolean;
}

export function NotificationChannelCard({
  channel,
  onEdit,
  onTest,
  onDelete,
  isDeleting,
}: NotificationChannelCardProps) {
  const [confirmDelete, setConfirmDelete] = useState(false);
  const Icon = CHANNEL_ICONS[channel.type] ?? Globe;

  return (
    <>
      <Card className="flex flex-col gap-3">
        <div className="flex items-start justify-between">
          <div className="flex items-center gap-3">
            <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-surface2">
              <Icon className="h-4 w-4 text-muted" />
            </div>
            <div>
              <p className="text-sm font-semibold text-ink">{channel.name}</p>
              <p className="text-xs text-muted">
                {CHANNEL_LABELS[channel.type] ?? channel.type}
              </p>
            </div>
          </div>
          {statusBadge(channel)}
        </div>

        {channel.error && (
          <p className="text-xs text-danger">{channel.error}</p>
        )}

        <div className="flex items-center justify-between text-[10px] text-muted">
          <span>Last sent: {timeAgo(channel.lastSentAt)}</span>
        </div>

        <div className="flex gap-2 border-t border-border pt-3">
          <Button variant="outline" size="sm" onClick={() => onEdit(channel)}>
            <Pencil className="mr-1 h-3 w-3" /> Edit
          </Button>
          <Button variant="outline" size="sm" onClick={() => onTest(channel)}>
            <Play className="mr-1 h-3 w-3" /> Test
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto text-danger hover:text-danger"
            onClick={() => setConfirmDelete(true)}
          >
            <Trash2 className="h-3 w-3" />
          </Button>
        </div>
      </Card>

      <ConfirmDialog
        open={confirmDelete}
        title="Delete Channel"
        message={`Delete notification channel "${channel.name}"? This cannot be undone and any routing rules referencing this channel will stop working.`}
        confirmLabel="Delete"
        confirmVariant="danger"
        isPending={isDeleting}
        onConfirm={() => {
          onDelete(channel.id);
          setConfirmDelete(false);
        }}
        onCancel={() => setConfirmDelete(false)}
      />
    </>
  );
}
