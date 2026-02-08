import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { X, Mail, Hash, Bell, Globe, Loader, Plus, Trash2 } from "lucide-react";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import { Badge } from "../ui/Badge";
import { cn } from "../../lib/utils";
import { notificationChannelSchema } from "../../lib/settingsSchemas";
import { useSaveNotificationChannel } from "../../hooks/useSettings";
import type { NotificationChannel, NotificationChannelType } from "../../api/types";

// ---------------------------------------------------------------------------
// Provider definitions
// ---------------------------------------------------------------------------

interface ChannelTypeDef {
  id: NotificationChannelType;
  label: string;
  icon: typeof Mail;
}

const CHANNEL_TYPES: ChannelTypeDef[] = [
  { id: "email", label: "Email", icon: Mail },
  { id: "slack", label: "Slack", icon: Hash },
  { id: "pagerduty", label: "PagerDuty", icon: Bell },
  { id: "webhook", label: "Webhook", icon: Globe },
];

// ---------------------------------------------------------------------------
// Form type
// ---------------------------------------------------------------------------

interface ChannelForm {
  name: string;
  type: NotificationChannelType;
  config: Record<string, unknown>;
  enabled: boolean;
}

// ---------------------------------------------------------------------------
// Dynamic config fields
// ---------------------------------------------------------------------------

function EmailConfigFields({
  config,
  onChange,
}: {
  config: Record<string, unknown>;
  onChange: (c: Record<string, unknown>) => void;
}) {
  const recipients = (config.recipients as string) ?? "";
  const smtpHost = (config.smtpHost as string) ?? "";

  return (
    <div className="space-y-3">
      <div>
        <label className="mb-1 block text-xs font-semibold text-muted">
          Recipients
        </label>
        <Input
          placeholder="comma-separated emails"
          value={recipients}
          onChange={(e) => onChange({ ...config, recipients: e.target.value })}
        />
        <p className="mt-1 text-[10px] text-muted">
          Separate multiple addresses with commas.
        </p>
      </div>
      <div>
        <label className="mb-1 block text-xs font-semibold text-muted">
          SMTP Host (optional)
        </label>
        <Input
          placeholder="smtp.example.com:587"
          value={smtpHost}
          onChange={(e) => onChange({ ...config, smtpHost: e.target.value })}
        />
      </div>
    </div>
  );
}

function SlackConfigFields({
  config,
  onChange,
}: {
  config: Record<string, unknown>;
  onChange: (c: Record<string, unknown>) => void;
}) {
  const webhookUrl = (config.webhookUrl as string) ?? "";
  const channel = (config.channel as string) ?? "";

  return (
    <div className="space-y-3">
      <div>
        <label className="mb-1 block text-xs font-semibold text-muted">
          Webhook URL
        </label>
        <Input
          placeholder="https://hooks.slack.com/services/..."
          value={webhookUrl}
          onChange={(e) => onChange({ ...config, webhookUrl: e.target.value })}
        />
      </div>
      <div>
        <label className="mb-1 block text-xs font-semibold text-muted">
          Channel
        </label>
        <Input
          placeholder="#alerts"
          value={channel}
          onChange={(e) => onChange({ ...config, channel: e.target.value })}
        />
      </div>
    </div>
  );
}

function PagerDutyConfigFields({
  config,
  onChange,
}: {
  config: Record<string, unknown>;
  onChange: (c: Record<string, unknown>) => void;
}) {
  const routingKey = (config.routingKey as string) ?? "";
  const severity = (config.severity as string) ?? "critical";

  return (
    <div className="space-y-3">
      <div>
        <label className="mb-1 block text-xs font-semibold text-muted">
          Routing Key
        </label>
        <Input
          placeholder="PagerDuty integration routing key"
          value={routingKey}
          onChange={(e) => onChange({ ...config, routingKey: e.target.value })}
        />
      </div>
      <div>
        <label className="mb-1 block text-xs font-semibold text-muted">
          Default Severity
        </label>
        <div className="flex gap-2">
          {(["critical", "error", "warning", "info"] as const).map((s) => (
            <button
              key={s}
              type="button"
              className={cn(
                "rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors",
                severity === s
                  ? "border-accent bg-accent/10 text-accent"
                  : "border-border text-muted hover:text-ink",
              )}
              onClick={() => onChange({ ...config, severity: s })}
            >
              {s}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}

function WebhookConfigFields({
  config,
  onChange,
}: {
  config: Record<string, unknown>;
  onChange: (c: Record<string, unknown>) => void;
}) {
  const url = (config.url as string) ?? "";
  const authType = (config.authType as string) ?? "none";
  const authToken = (config.authToken as string) ?? "";
  const headers = (config.headers as { key: string; value: string }[]) ?? [];

  function setHeaders(h: { key: string; value: string }[]) {
    onChange({ ...config, headers: h });
  }

  return (
    <div className="space-y-3">
      <div>
        <label className="mb-1 block text-xs font-semibold text-muted">
          URL
        </label>
        <Input
          placeholder="https://example.com/webhook"
          value={url}
          onChange={(e) => onChange({ ...config, url: e.target.value })}
        />
      </div>

      <div>
        <label className="mb-1 block text-xs font-semibold text-muted">
          Authentication
        </label>
        <div className="flex gap-2">
          {(["none", "bearer", "basic"] as const).map((a) => (
            <button
              key={a}
              type="button"
              className={cn(
                "rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors",
                authType === a
                  ? "border-accent bg-accent/10 text-accent"
                  : "border-border text-muted hover:text-ink",
              )}
              onClick={() => onChange({ ...config, authType: a })}
            >
              {a === "none" ? "None" : a === "bearer" ? "Bearer" : "Basic"}
            </button>
          ))}
        </div>
        {authType !== "none" && (
          <Input
            className="mt-2"
            type="password"
            placeholder={authType === "bearer" ? "Bearer token" : "user:password"}
            value={authToken}
            onChange={(e) => onChange({ ...config, authToken: e.target.value })}
          />
        )}
      </div>

      <div>
        <label className="mb-1 block text-xs font-semibold text-muted">
          Custom Headers
        </label>
        <div className="space-y-1.5">
          {headers.map((h, i) => (
            <div key={i} className="flex gap-2">
              <Input
                className="flex-1"
                placeholder="Header name"
                value={h.key}
                onChange={(e) => {
                  const updated = [...headers];
                  updated[i] = { ...updated[i], key: e.target.value };
                  setHeaders(updated);
                }}
              />
              <Input
                className="flex-1"
                placeholder="Value"
                value={h.value}
                onChange={(e) => {
                  const updated = [...headers];
                  updated[i] = { ...updated[i], value: e.target.value };
                  setHeaders(updated);
                }}
              />
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setHeaders(headers.filter((_, j) => j !== i))}
              >
                <Trash2 className="h-3 w-3" />
              </Button>
            </div>
          ))}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setHeaders([...headers, { key: "", value: "" }])}
          >
            <Plus className="mr-1 h-3 w-3" /> Add Header
          </Button>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// NotificationChannelModal
// ---------------------------------------------------------------------------

interface NotificationChannelModalProps {
  channel?: NotificationChannel;
  onClose: () => void;
}

export function NotificationChannelModal({
  channel,
  onClose,
}: NotificationChannelModalProps) {
  const isEdit = !!channel;
  const saveChannel = useSaveNotificationChannel();

  const [channelConfig, setChannelConfig] = useState<Record<string, unknown>>(
    channel?.config ?? {},
  );

  const {
    register,
    handleSubmit,
    watch,
    setValue,
    formState: { errors },
  } = useForm<ChannelForm>({
    resolver: zodResolver(notificationChannelSchema),
    defaultValues: {
      name: channel?.name ?? "",
      type: channel?.type ?? "email",
      config: channel?.config ?? {},
      enabled: channel?.enabled ?? true,
    },
  });

  const selectedType = watch("type");

  function onSubmit(data: ChannelForm) {
    const id = channel?.id ?? `ch-${crypto.randomUUID().slice(0, 8)}`;
    saveChannel.mutate(
      {
        id,
        name: data.name,
        type: data.type,
        config: channelConfig,
        enabled: data.enabled,
        lastSentAt: channel?.lastSentAt,
        error: undefined,
      },
      { onSuccess: onClose },
    );
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="surface-card w-full max-w-lg rounded-3xl p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h3 className="font-display text-lg font-semibold text-ink">
            {isEdit ? "Edit Channel" : "Create Channel"}
          </h3>
          <button
            type="button"
            onClick={onClose}
            className="rounded-full p-1 hover:bg-surface2"
          >
            <X className="h-4 w-4 text-muted" />
          </button>
        </div>

        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
          {/* Name */}
          <div>
            <label className="mb-1 block text-xs font-semibold text-muted">
              Channel Name
            </label>
            <Input placeholder="e.g. Ops Slack" {...register("name")} />
            {errors.name && (
              <p className="mt-1 text-xs text-danger">{errors.name.message}</p>
            )}
          </div>

          {/* Type selector */}
          <div>
            <label className="mb-2 block text-xs font-semibold text-muted">
              Type
            </label>
            <div className="grid grid-cols-4 gap-2">
              {CHANNEL_TYPES.map((ct) => {
                const Icon = ct.icon;
                return (
                  <button
                    key={ct.id}
                    type="button"
                    className={cn(
                      "flex flex-col items-center gap-1 rounded-xl border px-3 py-3 text-xs font-semibold transition-colors",
                      selectedType === ct.id
                        ? "border-accent bg-accent/10 text-accent"
                        : "border-border text-muted hover:text-ink hover:border-ink/20",
                    )}
                    onClick={() => {
                      setValue("type", ct.id);
                      setChannelConfig({});
                    }}
                  >
                    <Icon className="h-4 w-4" />
                    {ct.label}
                  </button>
                );
              })}
            </div>
          </div>

          {/* Dynamic config fields */}
          {selectedType === "email" && (
            <EmailConfigFields config={channelConfig} onChange={setChannelConfig} />
          )}
          {selectedType === "slack" && (
            <SlackConfigFields config={channelConfig} onChange={setChannelConfig} />
          )}
          {selectedType === "pagerduty" && (
            <PagerDutyConfigFields config={channelConfig} onChange={setChannelConfig} />
          )}
          {selectedType === "webhook" && (
            <WebhookConfigFields config={channelConfig} onChange={setChannelConfig} />
          )}

          {/* Enabled toggle */}
          <label className="flex items-center gap-2 text-xs">
            <input
              type="checkbox"
              className="rounded border-border"
              {...register("enabled")}
            />
            <span className="font-medium text-ink">Enabled</span>
          </label>

          {/* Actions */}
          <div className="flex justify-end gap-3 border-t border-border pt-3">
            <Button type="button" variant="ghost" size="sm" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={saveChannel.isPending}>
              {saveChannel.isPending ? (
                <>
                  <Loader className="mr-1.5 h-3 w-3 animate-spin" /> Saving...
                </>
              ) : isEdit ? (
                "Save Changes"
              ) : (
                "Create Channel"
              )}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
