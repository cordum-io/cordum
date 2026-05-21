import { useEffect } from "react";
import { useNavigate } from "react-router-dom";
import type { User } from "../api/types";
import { useConfigStore, type AuthCredentials } from "../state/config";
import { useUiStore } from "../state/ui";

// ---------------------------------------------------------------------------
// BroadcastChannel cross-tab sync
// ---------------------------------------------------------------------------

type SyncMessage =
  | { type: "auth-logout" }
  | { type: "auth-login"; creds: AuthCredentials; user: User }
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
            if (msg.creds && msg.user) {
              useConfigStore.getState().login(msg.creds, msg.user);
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

    // localStorage fallback for browsers without BroadcastChannel.
    //
    // The active code paths now write user metadata (cordum-user) but NEVER
    // write the auth token to localStorage (session cookies sync across tabs
    // automatically via the browser; apikey mode keeps the key in memory).
    // Watch cordum-user instead of the legacy cordum-api-key: any tab that
    // logs in/out toggles that key, and the cookie carries the actual auth.
    //
    // Since we can't recover the original creds shape from a storage event,
    // we conservatively assume session mode on user-write (cookie is the auth
    // artefact); a tab that wanted apikey mode is handled by the primary
    // BroadcastChannel path above.
    function onStorage(e: StorageEvent) {
      if (e.key === "cordum-user") {
        if (!e.newValue) {
          handleMessage({ type: "auth-logout" });
        } else {
          try {
            const user = JSON.parse(e.newValue) as User;
            if (user && user.id) {
              handleMessage({
                type: "auth-login",
                creds: { mode: "session" },
                user,
              });
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
