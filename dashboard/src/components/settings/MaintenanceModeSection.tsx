import { useEffect, useState } from "react";
import {
  AlertTriangle,
  Calendar,
  ChevronDown,
  ChevronUp,
  Clock,
  Power,
  Trash2,
} from "lucide-react";
import { Card } from "../ui/Card";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import { Textarea } from "../ui/Textarea";
import { ConfirmDialog } from "../ui/ConfirmDialog";
import { cn, formatDuration } from "../../lib/utils";
import { useGeneralConfig, useSetGeneralConfig } from "../../hooks/useSettings";
import type {
  GeneralConfig,
  MaintenanceWindow,
  MaintenanceSchedule,
} from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatElapsed(startIso: string): string {
  const ms = Date.now() - new Date(startIso).getTime();
  const mins = Math.floor(ms / 60_000);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  const remainMins = mins % 60;
  if (hrs < 24) return `${hrs}h ${remainMins}m`;
  const days = Math.floor(hrs / 24);
  return `${days}d ${hrs % 24}h`;
}


function formatDateTime(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

const DAY_LABELS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

// ---------------------------------------------------------------------------
// Elapsed timer (updates every minute)
// ---------------------------------------------------------------------------

function ElapsedTimer({ startedAt }: { startedAt: string }) {
  const [, setTick] = useState(0);

  useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), 60_000);
    return () => clearInterval(id);
  }, []);

  return <span className="font-mono text-sm text-danger">{formatElapsed(startedAt)}</span>;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function MaintenanceModeSection() {
  const { data: config } = useGeneralConfig();
  const saveConfig = useSetGeneralConfig();

  const [confirmOn, setConfirmOn] = useState(false);
  const [confirmOff, setConfirmOff] = useState(false);
  const [message, setMessage] = useState(config?.maintenanceMessage ?? "");
  const [showSchedule, setShowSchedule] = useState(false);
  const [showHistory, setShowHistory] = useState(false);

  // Schedule form state
  const [schedStart, setSchedStart] = useState("");
  const [schedEnd, setSchedEnd] = useState("");
  const [schedMsg, setSchedMsg] = useState("");
  const [isRecurring, setIsRecurring] = useState(false);
  const [recurDays, setRecurDays] = useState<number[]>([]);
  const [recurStartHour, setRecurStartHour] = useState(2);
  const [recurEndHour, setRecurEndHour] = useState(4);

  if (!config) return null;

  const isActive = config.maintenanceMode;
  const history = config.maintenanceHistory ?? [];
  const schedule = config.maintenanceSchedule ?? [];

  const handleToggleOn = () => {
    saveConfig.mutate({
      maintenanceMode: true,
      maintenanceStartedAt: new Date().toISOString(),
      maintenanceMessage: message || undefined,
    });
    setConfirmOn(false);
  };

  const handleToggleOff = () => {
    const entry: MaintenanceWindow = {
      startedAt: config.maintenanceStartedAt ?? new Date().toISOString(),
      endedAt: new Date().toISOString(),
      durationMs: config.maintenanceStartedAt
        ? Date.now() - new Date(config.maintenanceStartedAt).getTime()
        : 0,
      message: config.maintenanceMessage,
    };
    saveConfig.mutate({
      maintenanceMode: false,
      maintenanceStartedAt: undefined,
      maintenanceMessage: undefined,
      maintenanceHistory: [entry, ...history].slice(0, 5),
    });
    setConfirmOff(false);
  };

  const handleSaveMessage = () => {
    saveConfig.mutate({ maintenanceMessage: message || undefined });
  };

  const handleAddSchedule = () => {
    if (!schedStart || !schedEnd) return;
    const newSched: MaintenanceSchedule = {
      id: `sched-${Date.now()}`,
      startAt: schedStart,
      endAt: schedEnd,
      message: schedMsg || undefined,
      recurring: isRecurring && recurDays.length > 0
        ? { daysOfWeek: recurDays, startHour: recurStartHour, endHour: recurEndHour }
        : undefined,
    };
    saveConfig.mutate({
      maintenanceSchedule: [...schedule, newSched],
    });
    setSchedStart("");
    setSchedEnd("");
    setSchedMsg("");
    setIsRecurring(false);
    setRecurDays([]);
  };

  const handleRemoveSchedule = (id: string) => {
    saveConfig.mutate({
      maintenanceSchedule: schedule.filter((s) => s.id !== id),
    });
  };

  const toggleDay = (day: number) => {
    setRecurDays((prev) =>
      prev.includes(day) ? prev.filter((d) => d !== day) : [...prev, day].sort(),
    );
  };

  const nowIso = new Date().toISOString().slice(0, 16);

  return (
    <Card className={cn(isActive && "border-2 border-danger/50")}>
      {/* Header with toggle */}
      <div className="flex items-start justify-between">
        <div className="flex items-start gap-3">
          <div className={cn("mt-1 h-3 w-3 rounded-full", isActive ? "bg-danger animate-pulse" : "bg-success")} />
          <div>
            <h3 className="font-display text-base font-semibold text-ink">Maintenance Mode</h3>
            <p className="text-xs text-muted-foreground">
              {isActive
                ? "System is in maintenance — new jobs are rejected"
                : "System is operational"}
            </p>
          </div>
        </div>
        <Button
          variant={isActive ? "danger" : "outline"}
          size="sm"
          type="button"
          onClick={() => (isActive ? setConfirmOff(true) : setConfirmOn(true))}
          disabled={saveConfig.isPending}
        >
          <Power className="h-3.5 w-3.5" />
          {isActive ? "Disable" : "Enable"}
        </Button>
      </div>

      {/* Active maintenance details */}
      {isActive && (
        <div className="mt-4 space-y-3 rounded-xl border border-danger/20 bg-danger/5 p-4">
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Clock className="h-3.5 w-3.5" />
              Started {config.maintenanceStartedAt ? formatDateTime(config.maintenanceStartedAt) : "—"}
            </div>
            {config.maintenanceStartedAt && (
              <div className="flex items-center gap-2 text-xs">
                <AlertTriangle className="h-3.5 w-3.5 text-danger" />
                Elapsed: <ElapsedTimer startedAt={config.maintenanceStartedAt} />
              </div>
            )}
          </div>

          <div className="space-y-1">
            <label className="text-xs font-semibold text-muted-foreground">Maintenance Message</label>
            <div className="flex gap-2">
              <Textarea
                value={message}
                onChange={(e) => setMessage(e.target.value)}
                placeholder="Optional message shown to users..."
                rows={2}
                className="flex-1"
              />
              <Button
                variant="outline"
                size="sm"
                type="button"
                onClick={handleSaveMessage}
                disabled={message === (config.maintenanceMessage ?? "") || saveConfig.isPending}
              >
                Update
              </Button>
            </div>
          </div>
        </div>
      )}

      {/* Scheduled maintenance */}
      <div className="mt-4">
        <button
          type="button"
          onClick={() => setShowSchedule((v) => !v)}
          className="flex w-full items-center justify-between rounded-lg px-1 py-2 text-xs font-semibold text-muted-foreground hover:text-ink"
        >
          <span className="flex items-center gap-2">
            <Calendar className="h-3.5 w-3.5" />
            Scheduled Maintenance ({schedule.length})
          </span>
          {showSchedule ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}
        </button>

        {showSchedule && (
          <div className="mt-2 space-y-3">
            {/* Existing schedules */}
            {schedule.map((s) => (
              <div key={s.id} className="flex items-center justify-between rounded-lg border border-border bg-surface2/50 px-3 py-2 text-xs">
                <div>
                  <span className="font-medium text-ink">
                    {formatDateTime(s.startAt)} — {formatDateTime(s.endAt)}
                  </span>
                  {s.message && <span className="ml-2 text-muted-foreground">"{s.message}"</span>}
                  {s.recurring && (
                    <Badge variant="info" className="ml-2 text-xs">
                      Recurring: {s.recurring.daysOfWeek.map((d) => DAY_LABELS[d]).join(", ")}
                    </Badge>
                  )}
                </div>
                <button
                  type="button"
                  onClick={() => handleRemoveSchedule(s.id)}
                  className="rounded-full p-1 text-muted-foreground hover:text-danger"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </div>
            ))}

            {schedule.length === 0 && (
              <p className="text-xs text-muted-foreground italic">No scheduled maintenance windows</p>
            )}

            {/* Add schedule form */}
            <div className="space-y-3 rounded-xl border border-border bg-surface2/30 p-3">
              <p className="text-xs font-semibold text-ink">Schedule New Window</p>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="space-y-1">
                  <label className="text-xs font-semibold text-muted-foreground">Start</label>
                  <Input
                    type="datetime-local"
                    value={schedStart}
                    onChange={(e) => setSchedStart(e.target.value)}
                    min={nowIso}
                  />
                </div>
                <div className="space-y-1">
                  <label className="text-xs font-semibold text-muted-foreground">End</label>
                  <Input
                    type="datetime-local"
                    value={schedEnd}
                    onChange={(e) => setSchedEnd(e.target.value)}
                    min={schedStart || nowIso}
                  />
                </div>
              </div>

              <Input
                value={schedMsg}
                onChange={(e) => setSchedMsg(e.target.value)}
                placeholder="Optional message"
              />

              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={isRecurring}
                  onChange={(e) => setIsRecurring(e.target.checked)}
                  className="h-4 w-4 rounded border-border text-accent focus:ring-accent"
                />
                <span className="text-xs font-medium text-ink">Recurring</span>
              </label>

              {isRecurring && (
                <div className="space-y-2">
                  <div className="flex flex-wrap gap-1">
                    {DAY_LABELS.map((label, idx) => (
                      <button
                        key={label}
                        type="button"
                        onClick={() => toggleDay(idx)}
                        className={cn(
                          "rounded-full px-3 py-1 text-xs font-medium transition",
                          recurDays.includes(idx)
                            ? "bg-accent text-primary-foreground"
                            : "border border-border text-ink hover:bg-surface2",
                        )}
                      >
                        {label}
                      </button>
                    ))}
                  </div>
                  <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <Input
                      type="number"
                      value={recurStartHour}
                      onChange={(e) => setRecurStartHour(Number(e.target.value))}
                      min={0}
                      max={23}
                      className="w-16"
                    />
                    <span>:00 —</span>
                    <Input
                      type="number"
                      value={recurEndHour}
                      onChange={(e) => setRecurEndHour(Number(e.target.value))}
                      min={0}
                      max={23}
                      className="w-16"
                    />
                    <span>:00</span>
                  </div>
                </div>
              )}

              <Button
                variant="outline"
                size="sm"
                type="button"
                onClick={handleAddSchedule}
                disabled={!schedStart || !schedEnd || saveConfig.isPending}
              >
                <Calendar className="h-3.5 w-3.5" />
                Add Schedule
              </Button>
            </div>
          </div>
        )}
      </div>

      {/* Maintenance history */}
      <div className="mt-2">
        <button
          type="button"
          onClick={() => setShowHistory((v) => !v)}
          className="flex w-full items-center justify-between rounded-lg px-1 py-2 text-xs font-semibold text-muted-foreground hover:text-ink"
        >
          <span className="flex items-center gap-2">
            <Clock className="h-3.5 w-3.5" />
            History ({history.length})
          </span>
          {showHistory ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}
        </button>

        {showHistory && (
          <div className="mt-1 space-y-2">
            {history.length === 0 && (
              <p className="text-xs text-muted-foreground italic">No maintenance history</p>
            )}
            {history.map((w, i) => (
              <div key={i} className="flex items-center justify-between rounded-lg border border-border bg-surface2/50 px-3 py-2 text-xs">
                <div className="space-y-0.5">
                  <div className="font-medium text-ink">
                    {formatDateTime(w.startedAt)} — {formatDateTime(w.endedAt)}
                  </div>
                  {w.message && <div className="text-muted-foreground">"{w.message}"</div>}
                </div>
                <Badge variant="default">{formatDuration(w.durationMs)}</Badge>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Confirm dialogs */}
      <ConfirmDialog
        open={confirmOn}
        title="Enable Maintenance Mode"
        message="This will reject all new jobs and show a maintenance banner to all dashboard users. Existing running jobs will continue."
        confirmLabel="Enable Maintenance"
        confirmVariant="danger"
        isPending={saveConfig.isPending}
        onConfirm={handleToggleOn}
        onCancel={() => setConfirmOn(false)}
      />

      <ConfirmDialog
        open={confirmOff}
        title="Disable Maintenance Mode"
        message="This will restore normal operation. New jobs will be accepted again."
        confirmLabel="Disable Maintenance"
        confirmVariant="primary"
        isPending={saveConfig.isPending}
        onConfirm={handleToggleOff}
        onCancel={() => setConfirmOff(false)}
      />
    </Card>
  );
}
