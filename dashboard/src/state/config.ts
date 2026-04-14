import { create } from "zustand";
import { logger } from "../lib/logger";
import { broadcastSync } from "../hooks/useCrossTabSync";
import { useEventStore } from "./events";
import type { User } from "../api/types";

// ---------------------------------------------------------------------------
// React Query client reference — set once by App.tsx to avoid circular import
// ---------------------------------------------------------------------------

let _queryClient: { clear: () => void; cancelQueries: () => void } | null = null;

/** Called by App.tsx to register the QueryClient for cache clearing on logout/tenant-switch. */
export function registerQueryClient(qc: { clear: () => void; cancelQueries: () => void }): void {
  _queryClient = qc;
}

// ---------------------------------------------------------------------------
// Persistence helpers
// ---------------------------------------------------------------------------

// SECURITY: API key is NOT stored in localStorage. Authentication uses httpOnly
// cookies set by the gateway on login. The apiKey field in Zustand is memory-only
// (for backward compat with embedded API key mode and X-API-Key header fallback).
const TOKEN_KEY = "cordum-api-key"; // legacy — cleared on login, never written
const USER_KEY = "cordum-user";
const LOGIN_TS_KEY = "cordum-login-ts";

function clearLegacyToken(): void {
  if (typeof window !== "undefined") {
    window.localStorage.removeItem(TOKEN_KEY);
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
  isLoggingOut: boolean;
  loginTimestamp: number | null;
  /** @internal Prevents tenant impersonation via store mutation after login. */
  tenantLocked: boolean;

  /** True once runtime config has been loaded (from public/config.json or defaults). */
  loaded: boolean;

  // Actions
  update: (patch: ConfigPatch) => void;
  login: (token: string, user: User) => void;
  logout: () => void;
  refreshLoginTimestamp: () => void;
}

export const useConfigStore = create<ConfigState>((set, get) => {
  const savedUser = loadUser();
  // Clear any legacy token from localStorage (migrated to httpOnly cookie auth)
  clearLegacyToken();
  return {
    apiBaseUrl: "",
    apiKey: "",
    tenantId: savedUser?.tenant ?? "",
    principalId: savedUser?.id ?? "",
    principalRole: savedUser?.roles?.[0] ?? "",
    traceUrlTemplate: "",
    approvalSlaMs: 900_000, // 15 minutes default
    user: savedUser,
    isAuthenticated: !!savedUser,
    isLoggingOut: false,
    loginTimestamp: loadLoginTimestamp(),
    tenantLocked: !!(savedUser?.tenant),
    loaded: true,

    update: (patch) =>
      set((s) => {
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
        // Reset event store and query cache on tenant switch to prevent cross-tenant data leakage.
        // Order matters: cancel in-flight queries BEFORE clearing cache and applying new tenant.
        if (patch.tenantId !== undefined && patch.tenantId !== s.tenantId) {
          _queryClient?.cancelQueries();
          _queryClient?.clear();
          useEventStore.getState().reset();
        }
        const next = { ...s, ...patch };
        const locked = s.tenantLocked || !!(next.tenantId);
        return { ...next, isAuthenticated: !!next.apiKey, tenantLocked: locked };
      }),

    login: (token, user) => {
      logger.info("config-store", "Login", { userId: user.id, tenant: user.tenant });
      const now = Date.now();
      // Token stays in memory only — auth cookie set by server handles persistence.
      clearLegacyToken();
      persistUser(user);
      persistLoginTimestamp(now);
      set({
        apiKey: token,
        user,
        isAuthenticated: true,
        isLoggingOut: false,
        loginTimestamp: now,
        tenantId: user.tenant ?? "",
        principalId: user.id ?? "",
        principalRole: user.roles?.[0] ?? "",
        tenantLocked: !!(user.tenant),
      });
      broadcastSync({ type: "auth-login", token, user });
    },

    logout: () => {
      const alreadyLoggingOut = get().isLoggingOut;
      if (alreadyLoggingOut) {
        logger.debug("config-store", "Logout skipped; already in progress");
      } else {
        logger.info("config-store", "Logout");
      }
      clearLegacyToken();
      persistUser(null);
      persistLoginTimestamp(null);
      useEventStore.getState().reset();
      // Clear React Query cache to prevent cross-tenant/cross-user data leakage
      _queryClient?.clear();
      set({
        apiKey: "",
        user: null,
        isAuthenticated: false,
        isLoggingOut: true,
        loginTimestamp: null,
        tenantId: "",
        principalId: "",
        principalRole: "",
        tenantLocked: false,
      });
      if (!alreadyLoggingOut) {
        broadcastSync({ type: "auth-logout" });
      }
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
