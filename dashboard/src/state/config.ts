import { create } from "zustand";
import { logger } from "../lib/logger";
import { broadcastSync } from "../hooks/useCrossTabSync";
import type { User } from "../api/types";

// ---------------------------------------------------------------------------
// Persistence helpers
// ---------------------------------------------------------------------------

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

    update: (patch) =>
      set((s) => {
        if (patch.apiKey !== undefined) {
          persistToken(patch.apiKey);
        }
        const next = { ...s, ...patch };
        return { ...next, isAuthenticated: !!next.apiKey };
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
      });
      broadcastSync({ type: "auth-login" });
    },

    logout: () => {
      logger.info("config-store", "Logout");
      persistToken("");
      persistUser(null);
      persistLoginTimestamp(null);
      set({
        apiKey: "",
        user: null,
        isAuthenticated: false,
        loginTimestamp: null,
        tenantId: "",
        principalId: "",
        principalRole: "",
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
