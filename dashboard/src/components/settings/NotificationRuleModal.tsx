import { useForm, Controller } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { X, Loader } from "lucide-react";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import { notificationRuleSchema, type NotificationRuleForm } from "../../lib/settingsSchemas";
import { useNotificationRules, useSaveNotificationRules } from "../../hooks/useSettings";
import type { NotificationChannel, NotificationRule } from "../../api/types";

// ---------------------------------------------------------------------------
// NotificationRuleModal
// ---------------------------------------------------------------------------

interface NotificationRuleModalProps {
  rule?: NotificationRule;
  channels: NotificationChannel[];
  onClose: () => void;
}

export function NotificationRuleModal({
  rule,
  channels,
  onClose,
}: NotificationRuleModalProps) {
  const isEdit = !!rule;
  const saveRules = useSaveNotificationRules();
  const { data: existingRules } = useNotificationRules();

  const {
    register,
    handleSubmit,
    control,
    formState: { errors },
  } = useForm<NotificationRuleForm>({
    resolver: zodResolver(notificationRuleSchema),
    defaultValues: {
      eventPattern: rule?.eventPattern ?? "",
      channelIds: rule?.channelIds ?? [],
      throttleMs: rule?.throttleMs ?? 0,
      muteUntil: rule?.muteUntil ?? "",
      enabled: rule?.enabled ?? true,
    },
  });

  function onSubmit(data: NotificationRuleForm) {
    const id = rule?.id ?? `rule-${crypto.randomUUID().slice(0, 8)}`;
    const newRule: NotificationRule = {
      id,
      eventPattern: data.eventPattern,
      channelIds: data.channelIds,
      throttleMs: data.throttleMs,
      muteUntil: data.muteUntil || undefined,
      enabled: data.enabled,
    };

    const existing = existingRules ?? [];
    const idx = existing.findIndex((r) => r.id === id);
    const updated = idx >= 0
      ? existing.map((r, i) => (i === idx ? newRule : r))
      : [...existing, newRule];

    saveRules.mutate(updated, { onSuccess: onClose });
  }

  // Convert throttleMs to minutes for display
  const throttleMinutes = (value: number | undefined) =>
    value ? Math.round(value / 60_000) : 0;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="surface-card w-full max-w-lg rounded-3xl p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h3 className="font-display text-lg font-semibold text-ink">
            {isEdit ? "Edit Routing Rule" : "Create Routing Rule"}
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
          {/* Event pattern */}
          <div>
            <label className="mb-1 block text-xs font-semibold text-muted">
              Event Pattern
            </label>
            <Input
              placeholder="e.g. policy.*, approval.critical"
              className="font-mono"
              {...register("eventPattern")}
            />
            {errors.eventPattern && (
              <p className="mt-1 text-xs text-danger">
                {errors.eventPattern.message}
              </p>
            )}
            <p className="mt-1 text-[10px] text-muted">
              Use glob patterns: * matches any segment, e.g. policy.* matches
              policy.created, policy.updated.
            </p>
          </div>

          {/* Channel multi-select */}
          <div>
            <label className="mb-2 block text-xs font-semibold text-muted">
              Notify Channels
            </label>
            <Controller
              name="channelIds"
              control={control}
              render={({ field }) => (
                <div className="space-y-1.5">
                  {channels.length === 0 ? (
                    <p className="text-xs text-muted">
                      No channels configured. Create a channel first.
                    </p>
                  ) : (
                    channels.map((ch) => (
                      <label
                        key={ch.id}
                        className="flex items-center gap-2 rounded-lg border border-border px-3 py-2 text-xs hover:bg-surface2/30"
                      >
                        <input
                          type="checkbox"
                          className="rounded border-border"
                          checked={field.value.includes(ch.id)}
                          onChange={(e) => {
                            if (e.target.checked) {
                              field.onChange([...field.value, ch.id]);
                            } else {
                              field.onChange(
                                field.value.filter((id) => id !== ch.id),
                              );
                            }
                          }}
                        />
                        <span className="font-medium text-ink">{ch.name}</span>
                        <span className="text-muted">({ch.type})</span>
                      </label>
                    ))
                  )}
                </div>
              )}
            />
            {errors.channelIds && (
              <p className="mt-1 text-xs text-danger">
                {errors.channelIds.message}
              </p>
            )}
          </div>

          {/* Throttle */}
          <div>
            <label className="mb-1 block text-xs font-semibold text-muted">
              Throttle (minutes)
            </label>
            <Controller
              name="throttleMs"
              control={control}
              render={({ field }) => (
                <Input
                  type="number"
                  min={0}
                  placeholder="0"
                  value={throttleMinutes(field.value)}
                  onChange={(e) => {
                    const mins = parseInt(e.target.value, 10) || 0;
                    field.onChange(mins * 60_000);
                  }}
                />
              )}
            />
            <p className="mt-1 text-[10px] text-muted">
              Minimum interval between notifications for this rule. 0 = no
              throttling.
            </p>
          </div>

          {/* Mute until */}
          <div>
            <label className="mb-1 block text-xs font-semibold text-muted">
              Mute Until (optional)
            </label>
            <Input
              type="datetime-local"
              {...register("muteUntil")}
            />
            <p className="mt-1 text-[10px] text-muted">
              Suppress notifications until this time. Leave empty for no mute.
            </p>
          </div>

          {/* Enabled */}
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
            <Button type="submit" size="sm" disabled={saveRules.isPending}>
              {saveRules.isPending ? (
                <>
                  <Loader className="mr-1.5 h-3 w-3 animate-spin" /> Saving...
                </>
              ) : isEdit ? (
                "Save Rule"
              ) : (
                "Create Rule"
              )}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
