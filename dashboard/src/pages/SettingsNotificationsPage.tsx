import { useState, useCallback } from "react";
import { Info, Plus } from "lucide-react";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { NotificationChannelCard } from "../components/settings/NotificationChannelCard";
import { NotificationChannelModal } from "../components/settings/NotificationChannelModal";
import { NotificationRulesTable } from "../components/settings/NotificationRulesTable";
import { NotificationRuleModal } from "../components/settings/NotificationRuleModal";
import {
  useNotificationChannels,
  useNotificationRules,
  useDeleteNotificationChannel,
  useSaveNotificationRules,
} from "../hooks/useSettings";
import { ConfirmDialog } from "../components/ui/ConfirmDialog";
import { logger } from "../lib/logger";
import type { NotificationChannel, NotificationRule } from "../api/types";
import { usePageTitle } from "../hooks/usePageTitle";

// ---------------------------------------------------------------------------
// SettingsNotificationsPage
// ---------------------------------------------------------------------------

export default function SettingsNotificationsPage() {
  usePageTitle("Settings - Notifications");
  const { data: channels, isLoading: channelsLoading } = useNotificationChannels();
  const { data: rules, isLoading: rulesLoading } = useNotificationRules();
  const deleteChannel = useDeleteNotificationChannel();
  const saveRules = useSaveNotificationRules();

  const [deleteChannelId, setDeleteChannelId] = useState<string | null>(null);

  // Modal state
  const [channelModal, setChannelModal] = useState<{
    open: boolean;
    channel?: NotificationChannel;
  }>({ open: false });

  const [ruleModal, setRuleModal] = useState<{
    open: boolean;
    rule?: NotificationRule;
  }>({ open: false });

  // Handlers
  const handleTestChannel = useCallback((ch: NotificationChannel) => {
    logger.info("notifications", `Test notification sent to ${ch.name}`, {
      type: ch.type,
    });
    // No backend endpoint yet — log and alert
    alert(`Test notification sent to "${ch.name}" (${ch.type}). Check console for details.`);
  }, []);

  const handleToggleRule = useCallback(
    (rule: NotificationRule) => {
      const updated = (rules ?? []).map((r) =>
        r.id === rule.id ? { ...r, enabled: !r.enabled } : r,
      );
      saveRules.mutate(updated);
    },
    [rules, saveRules],
  );

  const handleDeleteRule = useCallback(
    (id: string) => {
      const updated = (rules ?? []).filter((r) => r.id !== id);
      saveRules.mutate(updated);
    },
    [rules, saveRules],
  );

  if (channelsLoading || rulesLoading) {
    return (
      <div className="space-y-6">
        <Card className="animate-pulse">
          <div className="space-y-3">
            <div className="h-4 w-48 rounded bg-surface2" />
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              <div className="h-32 rounded-xl bg-surface2" />
              <div className="h-32 rounded-xl bg-surface2" />
            </div>
          </div>
        </Card>
        <Card className="animate-pulse">
          <div className="space-y-3">
            <div className="h-4 w-36 rounded bg-surface2" />
            <div className="h-24 rounded bg-surface2" />
          </div>
        </Card>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Info banner */}
      <div className="flex items-start gap-3 rounded-xl border border-accent/30 bg-accent/5 px-4 py-3">
        <Info className="mt-0.5 h-4 w-4 shrink-0 text-accent" />
        <p className="text-xs text-muted">
          Notification routing is configured locally. Connect a notification
          backend to enable delivery.
        </p>
      </div>

      {/* Channels section */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-ink">Channels</h3>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setChannelModal({ open: true })}
          >
            <Plus className="mr-1 h-3 w-3" /> Create Channel
          </Button>
        </div>

        {channels.length === 0 ? (
          <Card>
            <div className="py-8 text-center">
              <p className="text-sm text-muted">No channels configured.</p>
              <p className="mt-1 text-xs text-muted">
                Create a channel to start receiving notifications.
              </p>
            </div>
          </Card>
        ) : (
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            {channels.map((ch) => (
              <NotificationChannelCard
                key={ch.id}
                channel={ch}
                onEdit={(c) => setChannelModal({ open: true, channel: c })}
                onTest={handleTestChannel}
                onDelete={(id) => setDeleteChannelId(id)}
                isDeleting={deleteChannel.isPending}
              />
            ))}
          </div>
        )}
      </div>

      {/* Routing rules section */}
      <NotificationRulesTable
        rules={rules}
        channels={channels}
        onCreateRule={() => setRuleModal({ open: true })}
        onEditRule={(r) => setRuleModal({ open: true, rule: r })}
        onDeleteRule={handleDeleteRule}
        onToggleRule={handleToggleRule}
        isDeleting={saveRules.isPending}
      />

      {/* Modals */}
      {channelModal.open && (
        <NotificationChannelModal
          channel={channelModal.channel}
          onClose={() => setChannelModal({ open: false })}
        />
      )}

      {ruleModal.open && (
        <NotificationRuleModal
          rule={ruleModal.rule}
          channels={channels}
          onClose={() => setRuleModal({ open: false })}
        />
      )}

      <ConfirmDialog
        open={deleteChannelId !== null}
        title="Delete Channel?"
        message="This notification channel will be permanently removed. Any routing rules using it will stop delivering."
        confirmLabel="Delete"
        confirmVariant="danger"
        isPending={deleteChannel.isPending}
        onConfirm={() => {
          if (deleteChannelId) {
            deleteChannel.mutate(deleteChannelId, {
              onSuccess: () => setDeleteChannelId(null),
            });
          }
        }}
        onCancel={() => setDeleteChannelId(null)}
      />
    </div>
  );
}
