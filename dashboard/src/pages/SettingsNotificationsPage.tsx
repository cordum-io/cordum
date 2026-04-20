/*
 * DESIGN: "Control Surface" — Notification Settings
 * Truthful config-backed delivery channels and routing rules.
 */
import { useMemo, useState } from "react";
import { motion } from "framer-motion";
import {
  Bell,
  Hash,
  Mail,
  Plus,
  Trash2,
  Pencil,
  Globe,
  AlertTriangle,
} from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { NotificationChannelModal } from "@/components/settings/NotificationChannelModal";
import { NotificationRuleModal } from "@/components/settings/NotificationRuleModal";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { Tabs } from "@/components/ui/Tabs";
import {
  useDeleteNotificationChannel,
  useNotificationChannels,
  useNotificationRules,
  useSaveNotificationRules,
} from "@/hooks/useSettings";
import type { NotificationChannel, NotificationRule } from "@/api/types";
import { toast } from "sonner";

type ActiveTab = "rules" | "channels";

type DeleteTarget =
  | { type: "channel"; channel: NotificationChannel }
  | { type: "rule"; rule: NotificationRule };

function channelIcon(type: NotificationChannel["type"]) {
  switch (type) {
    case "email":
      return Mail;
    case "slack":
      return Hash;
    case "webhook":
      return Globe;
    case "pagerduty":
      return Bell;
  }
}

function channelSummary(channel: NotificationChannel): string {
  switch (channel.type) {
    case "email":
      return String(channel.config.recipients ?? "Recipients not configured");
    case "slack":
      return String(channel.config.channel ?? channel.config.webhookUrl ?? "Slack destination not configured");
    case "pagerduty":
      return String(channel.config.severity ?? "PagerDuty severity not configured");
    case "webhook":
      return String(channel.config.url ?? "Webhook URL not configured");
  }
}

function formatDateTime(raw?: string): string {
  if (!raw) return "Not recorded";
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return raw;
  return parsed.toLocaleString();
}

export default function SettingsNotificationsPage() {
  const [activeTab, setActiveTab] = useState<ActiveTab>("rules");
  const [editingChannel, setEditingChannel] = useState<NotificationChannel | null>(null);
  const [editingRule, setEditingRule] = useState<NotificationRule | null>(null);
  const [channelModalOpen, setChannelModalOpen] = useState(false);
  const [ruleModalOpen, setRuleModalOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<DeleteTarget | null>(null);

  const channelsQuery = useNotificationChannels();
  const rulesQuery = useNotificationRules();
  const deleteChannel = useDeleteNotificationChannel();
  const saveRules = useSaveNotificationRules();

  const channels = channelsQuery.data ?? [];
  const rules = rulesQuery.data ?? [];
  const isLoading = channelsQuery.isLoading || rulesQuery.isLoading;

  const channelNames = useMemo(
    () => new Map(channels.map((channel) => [channel.id, channel.name])),
    [channels],
  );

  if (channelsQuery.isError || rulesQuery.isError) {
    const error = channelsQuery.error ?? rulesQuery.error;
    const refetch = channelsQuery.refetch ?? rulesQuery.refetch;
    return (
      <ErrorBanner
        title="Unable to load notification settings"
        message={error instanceof Error ? error.message : "Failed to load notification config"}
        onRetry={() => {
          void refetch?.();
        }}
      />
    );
  }

  const handleDeleteConfirm = () => {
    if (!deleteTarget) return;

    if (deleteTarget.type === "channel") {
      deleteChannel.mutate(deleteTarget.channel.id, {
        onSuccess: () => {
          setDeleteTarget(null);
          toast.success("Channel removed");
        },
      });
      return;
    }

    const updatedRules = rules.filter((rule) => rule.id !== deleteTarget.rule.id);
    saveRules.mutate(updatedRules, {
      onSuccess: () => {
        setDeleteTarget(null);
        toast.success("Routing rule removed");
      },
    });
  };

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader
        title="Notifications"
        subtitle="Config-backed delivery channels and routing rules for alerting surfaces that already exist in this deployment."
      />

      <InfoBanner variant="info" title="Saved in system config">
        Notification channels and routing rules persist through the same config path as the rest of Settings. There is no separate live notification service API behind this page.
      </InfoBanner>

      <Tabs
        ariaLabel="Notification surfaces"
        variant="segmented"
        activeTab={activeTab}
        onChange={(id) => setActiveTab(id as ActiveTab)}
        tabs={[
          { id: "rules", label: "Routing rules", count: rules.length },
          { id: "channels", label: "Channels", count: channels.length },
        ]}
      />

      {isLoading ? (
        <div className="space-y-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <SkeletonCard key={i} />
          ))}
        </div>
      ) : activeTab === "channels" ? (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 className="text-sm font-display font-semibold text-foreground">Delivery channels</h2>
              <p className="mt-1 text-xs text-muted-foreground">
                These destinations receive notifications when one or more routing rules match an event.
              </p>
            </div>
            <Button
              size="sm"
              onClick={() => {
                setEditingChannel(null);
                setChannelModalOpen(true);
              }}
            >
              <Plus className="h-3.5 w-3.5" />
              Add channel
            </Button>
          </div>

          {channels.length === 0 ? (
            <EmptyState
              icon={<Bell className="h-8 w-8" />}
              title="No notification channels"
              description="Create an email, Slack, PagerDuty, or webhook destination before wiring routing rules."
            />
          ) : (
            <div className="space-y-3">
              {channels.map((channel, index) => {
                const Icon = channelIcon(channel.type);
                return (
                  <motion.div
                    key={channel.id}
                    initial={{ opacity: 0, y: 8 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ delay: index * 0.04 }}
                    className="instrument-card space-y-4"
                  >
                    <div className="flex flex-wrap items-start justify-between gap-3">
                      <div className="flex items-start gap-3">
                        <div className="mt-0.5 rounded-2xl bg-cordum/10 p-2 text-cordum">
                          <Icon className="h-4 w-4" />
                        </div>
                        <div>
                          <div className="flex flex-wrap items-center gap-2">
                            <h3 className="text-sm font-display font-semibold text-foreground">{channel.name}</h3>
                            <StatusBadge variant={channel.enabled ? "healthy" : "muted"}>
                              {channel.enabled ? "Enabled" : "Disabled"}
                            </StatusBadge>
                            <StatusBadge variant="info">{channel.type}</StatusBadge>
                          </div>
                          <p className="mt-1 text-xs font-mono text-muted-foreground">
                            {channelSummary(channel)}
                          </p>
                        </div>
                      </div>

                      <div className="flex items-center gap-2">
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => {
                            setEditingChannel(channel);
                            setChannelModalOpen(true);
                          }}
                        >
                          <Pencil className="h-3.5 w-3.5" />
                          Edit
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setDeleteTarget({ type: "channel", channel })}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                          Remove
                        </Button>
                      </div>
                    </div>

                    <div className="grid gap-3 md:grid-cols-2">
                      <div className="rounded-3xl border border-border bg-surface-1/70 p-4">
                        <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">Last sent</p>
                        <p className="mt-2 text-sm text-foreground">{formatDateTime(channel.lastSentAt)}</p>
                      </div>
                      <div className="rounded-3xl border border-border bg-surface-1/70 p-4">
                        <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">Last error</p>
                        <p className="mt-2 text-sm text-foreground">{channel.error || "None recorded"}</p>
                      </div>
                    </div>
                  </motion.div>
                );
              })}
            </div>
          )}
        </div>
      ) : (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 className="text-sm font-display font-semibold text-foreground">Routing rules</h2>
              <p className="mt-1 text-xs text-muted-foreground">
                Match event patterns to one or more channels, with optional throttling and mute windows.
              </p>
            </div>
            <Button
              size="sm"
              disabled={channels.length === 0}
              onClick={() => {
                setEditingRule(null);
                setRuleModalOpen(true);
              }}
            >
              <Plus className="h-3.5 w-3.5" />
              Add rule
            </Button>
          </div>

          {channels.length === 0 && (
            <InfoBanner variant="warning" title="Create a channel first">
              Routing rules can only target existing channels. Add at least one delivery channel before creating notification rules.
            </InfoBanner>
          )}

          {rules.length === 0 ? (
            <EmptyState
              icon={<AlertTriangle className="h-8 w-8" />}
              title="No routing rules"
              description="Create config-backed notification rules such as job.failed or approval.* once you know which channels should receive them."
            />
          ) : (
            <div className="space-y-3">
              {rules.map((rule, index) => (
                <motion.div
                  key={rule.id}
                  initial={{ opacity: 0, y: 8 }}
                  animate={{ opacity: 1, y: 0 }}
                  transition={{ delay: index * 0.04 }}
                  className="instrument-card space-y-4"
                >
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div>
                      <div className="flex flex-wrap items-center gap-2">
                        <h3 className="text-sm font-display font-semibold text-foreground">{rule.eventPattern}</h3>
                        <StatusBadge variant={rule.enabled ? "healthy" : "muted"}>
                          {rule.enabled ? "Enabled" : "Disabled"}
                        </StatusBadge>
                      </div>
                      <p className="mt-1 text-xs font-mono text-muted-foreground">
                        Rule ID: {rule.id}
                      </p>
                    </div>

                    <div className="flex items-center gap-2">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          setEditingRule(rule);
                          setRuleModalOpen(true);
                        }}
                      >
                        <Pencil className="h-3.5 w-3.5" />
                        Edit
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => setDeleteTarget({ type: "rule", rule })}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                        Remove
                      </Button>
                    </div>
                  </div>

                  <div className="grid gap-3 md:grid-cols-3">
                    <div className="rounded-3xl border border-border bg-surface-1/70 p-4">
                      <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">Channels</p>
                      <div className="mt-2 flex flex-wrap gap-2">
                        {rule.channelIds.length > 0 ? rule.channelIds.map((channelId) => (
                          <StatusBadge key={channelId} variant="info">
                            {channelNames.get(channelId) || channelId}
                          </StatusBadge>
                        )) : (
                          <span className="text-sm text-muted-foreground">None selected</span>
                        )}
                      </div>
                    </div>

                    <div className="rounded-3xl border border-border bg-surface-1/70 p-4">
                      <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">Throttle</p>
                      <p className="mt-2 text-sm text-foreground">
                        {rule.throttleMs ? `${Math.round(rule.throttleMs / 60_000)} min` : "Off"}
                      </p>
                    </div>

                    <div className="rounded-3xl border border-border bg-surface-1/70 p-4">
                      <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">Muted until</p>
                      <p className="mt-2 text-sm text-foreground">{formatDateTime(rule.muteUntil)}</p>
                    </div>
                  </div>
                </motion.div>
              ))}
            </div>
          )}
        </div>
      )}

      {channelModalOpen && (
        <NotificationChannelModal
          channel={editingChannel ?? undefined}
          onClose={() => {
            setChannelModalOpen(false);
            setEditingChannel(null);
          }}
        />
      )}

      {ruleModalOpen && (
        <NotificationRuleModal
          rule={editingRule ?? undefined}
          channels={channels}
          onClose={() => {
            setRuleModalOpen(false);
            setEditingRule(null);
          }}
        />
      )}

      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDeleteConfirm}
        title={deleteTarget?.type === "channel" ? "Remove notification channel?" : "Remove routing rule?"}
        description={
          deleteTarget?.type === "channel"
            ? "This removes the channel from saved config. Rules that still reference it will need to be edited."
            : "This removes the routing rule from saved config immediately."
        }
        confirmLabel={deleteTarget?.type === "channel" ? "Remove channel" : "Remove rule"}
        variant="destructive"
        isPending={deleteChannel.isPending || saveRules.isPending}
      />
    </motion.div>
  );
}
