import { useState, useRef, useEffect, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { motion, AnimatePresence } from "framer-motion";
import { Bell, CheckCircle2, XCircle, AlertTriangle, Info, X, Check } from "lucide-react";
import { useEventStore } from "@/state/events";
import { useDialogA11y } from "@/hooks/useDialogA11y";
import { formatRelativeTime } from "@/lib/utils";

interface Notification {
  id: string;
  type: "success" | "error" | "warning" | "info";
  title: string;
  message: string;
  timestamp: string;
  read: boolean;
}

function eventTypeToNotifType(eventType: string): Notification["type"] {
  if (eventType.includes("failed") || eventType.includes("error") || eventType.includes("cancel")) return "error";
  if (eventType.includes("safety") || eventType.includes("approval") || eventType.includes("alert")) return "warning";
  if (eventType.includes("succeeded") || eventType.includes("completed")) return "success";
  return "info";
}

function eventTypeToTitle(eventType: string): string {
  if (eventType.startsWith("job.result.failed")) return "Job Failed";
  if (eventType.startsWith("job.result.succeeded")) return "Job Succeeded";
  if (eventType.startsWith("job.result")) return "Job Result";
  if (eventType.startsWith("job.submit")) return "Job Submitted";
  if (eventType.startsWith("job.cancel")) return "Job Cancelled";
  if (eventType.startsWith("job.progress")) return "Job Progress";
  if (eventType.startsWith("worker.heartbeat")) return "Worker Heartbeat";
  if (eventType.startsWith("safety")) return "Safety Decision";
  if (eventType.startsWith("system.alert")) return "System Alert";
  return eventType;
}

function eventPayloadSummary(payload: Record<string, unknown>): string {
  const parts: string[] = [];
  if (payload.jobId) parts.push(`Job ${String(payload.jobId).slice(0, 12)}`);
  if (payload.workerId) parts.push(`Worker ${String(payload.workerId).slice(0, 12)}`);
  if (payload.topic) parts.push(String(payload.topic));
  if (payload.status) parts.push(String(payload.status));
  if (payload.errorMessage) parts.push(String(payload.errorMessage).slice(0, 80));
  if (payload.message) parts.push(String(payload.message).slice(0, 80));
  return parts.join(" · ") || "Event received";
}

const iconMap = {
  success: CheckCircle2,
  error: XCircle,
  warning: AlertTriangle,
  info: Info,
};

const colorMap = {
  success: "text-[var(--color-success)]",
  error: "text-destructive",
  warning: "text-[var(--color-warning)]",
  info: "text-[var(--color-info)]",
};

const bgMap = {
  success: "bg-[var(--color-success)]/10",
  error: "bg-destructive/10",
  warning: "bg-[var(--color-warning)]/10",
  info: "bg-[var(--color-info)]/10",
};

export function NotificationPopover() {
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [dismissed, setDismissed] = useState<Set<string>>(new Set());
  const [readIds, setReadIds] = useState<Set<string>>(new Set());
  const ref = useRef<HTMLDivElement>(null);

  const events = useEventStore((s) => s.events);

  // Derive notifications from real WebSocket events (skip heartbeats — too noisy)
  const notifications: Notification[] = useMemo(() => {
    return events
      .filter((e) => !e.type.startsWith("worker.heartbeat") && !dismissed.has(e.id))
      .slice(0, 20)
      .map((e) => ({
        id: e.id,
        type: eventTypeToNotifType(e.type),
        title: eventTypeToTitle(e.type),
        message: eventPayloadSummary(e.payload),
        timestamp: e.timestamp,
        read: readIds.has(e.id),
      }));
  }, [events, dismissed, readIds]);

  const unreadCount = notifications.filter((n) => !n.read).length;

  const dialogRef = useDialogA11y(() => setOpen(false));

  // Close on outside click
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    if (open) document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  const markAllRead = () => {
    setReadIds(new Set(notifications.map((n) => n.id)));
  };

  const dismiss = (id: string) => {
    setDismissed((prev) => new Set(prev).add(id));
  };

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen(!open)}
        className="relative p-2 rounded-md hover:bg-surface-2 transition-colors"
        aria-label="Notifications"
      >
        <Bell className="w-4 h-4 text-muted-foreground" />
        {unreadCount > 0 && (
          <>
            <span className="absolute top-1 right-1 w-2.5 h-2.5 rounded-full bg-[var(--color-warning)] border-2 border-surface-0 status-pulse" />
            <span className="sr-only">{unreadCount} unread notifications</span>
          </>
        )}
      </button>

      <AnimatePresence>
        {open && (
          <motion.div
            initial={{ opacity: 0, y: 4, scale: 0.97 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 4, scale: 0.97 }}
            transition={{ duration: 0.15 }}
            ref={dialogRef}
            role="dialog"
            aria-modal="true"
            aria-label="Notifications"
            className="absolute right-0 top-full mt-2 w-96 bg-surface-1 border border-border rounded-xl shadow-2xl overflow-hidden z-[90]"
          >
            {/* Header */}
            <div className="flex items-center justify-between px-4 py-3 border-b border-border">
              <div className="flex items-center gap-2">
                <h3 className="text-sm font-display font-semibold text-foreground">Notifications</h3>
                {unreadCount > 0 && (
                  <span className="inline-flex items-center justify-center w-5 h-5 rounded-full bg-cordum/20 text-cordum text-[10px] font-mono font-bold">
                    {unreadCount}
                  </span>
                )}
              </div>
              {unreadCount > 0 && (
                <button
                  onClick={markAllRead}
                  className="flex items-center gap-1 text-[11px] text-cordum hover:text-cordum-bright transition-colors"
                >
                  <Check className="w-3 h-3" />
                  Mark all read
                </button>
              )}
            </div>

            {/* Notification list */}
            <div className="max-h-96 overflow-y-auto">
              {notifications.length === 0 ? (
                <div className="px-4 py-10 text-center">
                  <Bell className="w-8 h-8 text-muted-foreground/40 mx-auto mb-2" />
                  <p className="text-sm text-muted-foreground">No notifications</p>
                </div>
              ) : (
                notifications.map((notif) => {
                  const Icon = iconMap[notif.type];
                  return (
                    <div
                      key={notif.id}
                      className={`flex gap-3 px-4 py-3 border-b border-border/50 transition-colors hover:bg-surface-2/50 ${
                        !notif.read ? "bg-surface-2/30" : ""
                      }`}
                    >
                      <div className={`shrink-0 w-7 h-7 rounded-lg ${bgMap[notif.type]} flex items-center justify-center mt-0.5`}>
                        <Icon className={`w-3.5 h-3.5 ${colorMap[notif.type]}`} />
                      </div>
                      <div className="flex-1 min-w-0">
                        <div className="flex items-start justify-between gap-2">
                          <p className="text-xs font-semibold text-foreground truncate">
                            {!notif.read && (
                              <span className="inline-block w-1.5 h-1.5 rounded-full bg-cordum mr-1.5 relative -top-px" />
                            )}
                            {notif.title}
                          </p>
                          <button
                            onClick={() => dismiss(notif.id)}
                            className="shrink-0 p-0.5 rounded hover:bg-surface-3 text-muted-foreground hover:text-foreground transition-colors"
                          >
                            <X className="w-3 h-3" />
                          </button>
                        </div>
                        <p className="text-[11px] text-muted-foreground mt-0.5 line-clamp-2">{notif.message}</p>
                        <p className="text-[10px] text-muted-foreground/60 font-mono mt-1">{formatRelativeTime(notif.timestamp)}</p>
                      </div>
                    </div>
                  );
                })
              )}
            </div>

            {/* Footer */}
            <div className="px-4 py-2.5 border-t border-border">
              <button
                onClick={() => { setOpen(false); navigate("/audit"); }}
                className="w-full text-center text-[11px] text-cordum hover:text-cordum-bright transition-colors font-medium"
              >
                View all notifications
              </button>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
