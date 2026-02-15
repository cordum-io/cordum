import { useEffect } from "react";
import { useNavigate } from "react-router-dom";
import type { User } from "../api/types";
import { useConfigStore } from "../state/config";
import { useUiStore } from "../state/ui";

// ---------------------------------------------------------------------------
// BroadcastChannel cross-tab sync
// ---------------------------------------------------------------------------

type SyncMessage =
  | { type: "auth-logout" }
  | { type: "auth-login"; token: string; user: User }
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
            if (msg.token && msg.user) {
              useConfigStore.getState().login(msg.token, msg.user);
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
          // Read user from localStorage since StorageEvent doesn't carry full payload
          const rawUser = window.localStorage.getItem("cordum-user");
          try {
            const user = rawUser ? (JSON.parse(rawUser) as User) : null;
            if (user) {
              handleMessage({ type: "auth-login", token: e.newValue, user });
            }
          } catch {
            // corrupt user data — ignore
          }
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
