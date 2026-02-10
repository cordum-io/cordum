import { useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { useConfigStore } from "../state/config";
import { useUiStore } from "../state/ui";

// ---------------------------------------------------------------------------
// BroadcastChannel cross-tab sync
// ---------------------------------------------------------------------------

type SyncMessage =
  | { type: "auth-logout" }
  | { type: "auth-login" }
  | { type: "theme-change"; theme: "light" | "dark" | "system" };

let channel: BroadcastChannel | null = null;
try {
  channel = new BroadcastChannel("cordum-sync");
} catch {
  // BroadcastChannel unsupported (e.g. older Safari) — falls back to storage events
}

/** Guard flag to prevent infinite ping-pong between tabs.
 *  When handling an incoming sync message, store actions (login/logout/toggleTheme)
 *  call broadcastSync again — the flag suppresses re-broadcasts during handling. */
let isSyncing = false;

/** Broadcast a sync message to other tabs. */
export function broadcastSync(msg: SyncMessage): void {
  if (isSyncing) return;
  try {
    channel?.postMessage(msg);
  } catch {
    // Channel closed or unavailable — ignore
  }
}

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

export function useCrossTabSync(): void {
  const navigate = useNavigate();

  useEffect(() => {
    function handleMessage(msg: SyncMessage) {
      isSyncing = true;
      try {
        switch (msg.type) {
          case "auth-logout":
            useConfigStore.getState().logout();
            navigate("/login", { replace: true });
            break;
          case "auth-login":
            // Reload auth state from localStorage
            {
              const token = window.localStorage.getItem("cordum-api-key") ?? "";
              const rawUser = window.localStorage.getItem("cordum-user");
              if (token && rawUser) {
                try {
                  const user = JSON.parse(rawUser);
                  useConfigStore.getState().login(token, user);
                } catch {
                  // corrupt data — ignore
                }
              }
            }
            break;
          case "theme-change":
            if (useUiStore.getState().theme !== msg.theme) {
              useUiStore.getState().setTheme(msg.theme);
            }
            break;
        }
      } finally {
        isSyncing = false;
      }
    }

    // BroadcastChannel listener
    function onBroadcast(e: MessageEvent<SyncMessage>) {
      handleMessage(e.data);
    }

    // localStorage fallback for browsers without BroadcastChannel
    function onStorage(e: StorageEvent) {
      if (e.key === "cordum-api-key") {
        if (!e.newValue) {
          handleMessage({ type: "auth-logout" });
        } else {
          handleMessage({ type: "auth-login" });
        }
      } else if (e.key === "cordum-theme" && e.newValue) {
        const theme = e.newValue as "light" | "dark" | "system";
        handleMessage({ type: "theme-change", theme });
      }
    }

    channel?.addEventListener("message", onBroadcast);
    window.addEventListener("storage", onStorage);

    return () => {
      channel?.removeEventListener("message", onBroadcast);
      window.removeEventListener("storage", onStorage);
    };
  }, [navigate]);
}
