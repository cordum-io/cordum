import { Wifi, WifiOff } from "lucide-react";
import { useEventStore } from "../state/events";
import { useSyncExternalStore } from "react";

type ConnectionStatus = "connected" | "reconnecting" | "disconnected";

const subscribeBrowserOnline = (cb: () => void) => {
  window.addEventListener("online", cb);
  window.addEventListener("offline", cb);
  return () => {
    window.removeEventListener("online", cb);
    window.removeEventListener("offline", cb);
  };
};
const getBrowserOnline = () => navigator.onLine;

export function ConnectionIndicator() {
  const wsStatus = useEventStore((s) => s.status);
  const browserOnline = useSyncExternalStore(subscribeBrowserOnline, getBrowserOnline);

  // Browser offline overrides everything; otherwise map WS status
  let status: ConnectionStatus;
  if (!browserOnline) {
    status = "disconnected";
  } else if (wsStatus === "connected") {
    status = "connected";
  } else if (wsStatus === "connecting" || wsStatus === "reconnecting") {
    status = "reconnecting";
  } else {
    status = "disconnected";
  }

  const config = {
    connected: {
      icon: Wifi,
      label: "All Systems Nominal",
      dotClass: "bg-[var(--color-success)] status-pulse",
      badgeClass: "bg-[var(--color-success)]/15 text-[var(--color-success)] border-[var(--color-success)]/20",
    },
    reconnecting: {
      icon: Wifi,
      label: "Reconnecting...",
      dotClass: "bg-[var(--color-warning)] animate-pulse",
      badgeClass: "bg-[var(--color-warning)]/15 text-[var(--color-warning)] border-[var(--color-warning)]/20",
    },
    disconnected: {
      icon: WifiOff,
      label: "Disconnected",
      dotClass: "bg-destructive",
      badgeClass: "bg-destructive/15 text-destructive border-destructive/20",
    },
  };

  const c = config[status];

  return (
    <span
      className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-mono font-medium border transition-all duration-300 ${c.badgeClass}`}
    >
      <span className={`w-1.5 h-1.5 rounded-full ${c.dotClass}`} />
      {c.label}
    </span>
  );
}
