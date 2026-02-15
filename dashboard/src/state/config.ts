import { create } from "zustand";
import { logger } from "../lib/logger";
import { broadcastSync } from "../hooks/useCrossTabSync";
import { useEventStore } from "./events";
import type { User } from "../api/types";

// ---------------------------------------------------------------------------
// Persistence helpers
// ---------------------------------------------------------------------------

// SECURITY NOTE: API key stored in localStorage for stateless SPA auth.
// Accepted risk — mitigated by: no dangerouslySetInnerHTML, X-Tenant-ID
// isolation, CSP headers, and HttpOnly not applicable (JS needs the token).
const TOKEN_KEY = "cordum-api-key";
const USER_KEY = "cordum-user";
const LOGIN_TS_KEY = "cordum-login-ts";

function loadToken(): string {
  if (typeof window !== "undefined") {
    return window.localStorage.getItem(TOKEN_KEY) ?? "";
  }
  return "";
}

function persistToken(key: string): void {
  if (typeof window !== "undefined") {
    if (key) {
      window.localStorage.setItem(TOKEN_KEY, key);
    } else {
      window.localStorage.removeItem(TOKEN_KEY);
    }
  }
}

function loadUser(): User | null {
  if (typeof window !== "undefined") {
    const raw = window.localStorage.getItem(USER_KEY);
    if (raw) {
      try {
        return JSON.parse(raw) as User;
      } catch {
        logger.warn("config-store", "Corrupt user data in localStorage, ignoring");
      }
    }
  }
  return null;
}

function persistUser(user: User | null): void {
  if (typeof window !== "undefined") {
    if (user) {
      window.localStorage.setItem(USER_KEY, JSON.stringify(user));
    } else {
      window.localStorage.removeItem(USER_KEY);
    }
  }
}

function loadLoginTimestamp(): number | null {
  if (typeof window !== "undefined") {
    const raw = window.localStorage.getItem(LOGIN_TS_KEY);
    if (raw) return Number(raw) || null;
  }
  return null;
}

function persistLoginTimestamp(ts: number | null): void {
  if (typeof window !== "undefined") {
    if (ts) {
      window.localStorage.setItem(LOGIN_TS_KEY, String(ts));
    } else {
      window.localStorage.removeItem(LOGIN_TS_KEY);
    }
  }
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

interface ConfigPatch {
  apiBaseUrl?: string;
  apiKey?: string;
  tenantId?: string;
  principalId?: string;
  principalRole?: string;
  traceUrlTemplate?: string;
  approvalSlaMs?: number;
}

interface ConfigState {
  // Connection
  apiBaseUrl: string;
  apiKey: string;
  tenantId: string;
  principalId: string;
  principalRole: string;
  traceUrlTemplate: string;

  // SLA
  approvalSlaMs: number;

  // Auth
  user: User | null;
  isAuthenticated: boolean;
  loginTimestamp: number | null;
  /** @internal Prevents tenant impersonation via store mutation after login. */
  tenantLocked: boolean;

  // Actions
  update: (patch: ConfigPatch) => void;
  login: (token: string, user: User) => void;
  logout: () => void;
  refreshLoginTimestamp: () => void;
}

export const useConfigStore = create<ConfigState>((set) => {
  const savedUser = loadUser();
  return {
    apiBaseUrl: "",
    apiKey: loadToken(),
    tenantId: savedUser?.tenant ?? "",
    principalId: savedUser?.id ?? "",
    principalRole: savedUser?.roles?.[0] ?? "",
    traceUrlTemplate: "",
    approvalSlaMs: 900_000, // 15 minutes default
    user: savedUser,
    isAuthenticated: !!loadToken(),
    loginTimestamp: loadLoginTimestamp(),
    tenantLocked: !!(savedUser?.tenant),

    update: (patch) =>
      set((s) => {
        if (patch.apiKey !== undefined) {
          persistToken(patch.apiKey);
        }
        // Defense-in-depth: prevent tenant impersonation via store mutation
        if (s.tenantLocked && patch.tenantId !== undefined && patch.tenantId !== s.tenantId) {
          logger.warn("config-store", "Blocked tenantId change while locked", {
            current: s.tenantId,
            attempted: patch.tenantId,
          });
          const { tenantId: _ignored, ...safePatch } = patch;
          const next = { ...s, ...safePatch };
          return { ...next, isAuthenticated: !!next.apiKey };
        }
        // Reset event store on tenant switch to prevent cross-tenant data leakage
        if (patch.tenantId !== undefined && patch.tenantId !== s.tenantId) {
          useEventStore.getState().reset();
        }
        const next = { ...s, ...patch };
        const locked = s.tenantLocked || !!(next.tenantId);
        return { ...next, isAuthenticated: !!next.apiKey, tenantLocked: locked };
      }),

    login: (token, user) => {
      logger.info("config-store", "Login", { userId: user.id, tenant: user.tenant });
      const now = Date.now();
      persistToken(token);
      persistUser(user);
      persistLoginTimestamp(now);
      set({
        apiKey: token,
        user,
        isAuthenticated: true,
        loginTimestamp: now,
        tenantId: user.tenant ?? "",
        principalId: user.id ?? "",
        principalRole: user.roles?.[0] ?? "",
        tenantLocked: !!(user.tenant),
      });
      broadcastSync({ type: "auth-login", token, user });
    },

    logout: () => {
      logger.info("config-store", "Logout");
      persistToken("");
      persistUser(null);
      persistLoginTimestamp(null);
      useEventStore.getState().reset();
      set({
        apiKey: "",
        user: null,
        isAuthenticated: false,
        loginTimestamp: null,
        tenantId: "",
        principalId: "",
        principalRole: "",
        tenantLocked: false,
      });
      broadcastSync({ type: "auth-logout" });
    },

    refreshLoginTimestamp: () => {
      const now = Date.now();
      persistLoginTimestamp(now);
      set({ loginTimestamp: now });
    },
  };
});

// ---------------------------------------------------------------------------
// SLA helpers
// ---------------------------------------------------------------------------

export function isSlaBreach(waitMs: number, slaMs: number): boolean {
  return waitMs > slaMs;
}

export function slaRemainingMs(waitMs: number, slaMs: number): number {
  return slaMs - waitMs;
}
