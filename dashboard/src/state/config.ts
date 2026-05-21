import { create } from "zustand";
import { logger } from "../lib/logger";
import { broadcastSync } from "../hooks/useCrossTabSync";
import { useEventStore } from "./events";
import { clearVerificationState } from "./verification";
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
// Auth modes
// ---------------------------------------------------------------------------
//
// The gateway accepts two parallel auth mechanisms:
//
//   - "apikey":   long-lived pre-issued key, sent as `X-API-Key: <key>`.
//   - "session":  short-lived token from password/SSO login. The gateway
//                 sets an httpOnly `cordum_session` cookie that the browser
//                 sends automatically on every fetch (`credentials: "include"`).
//                 The dashboard NEVER stores the session token as `apiKey`;
//                 sending a session token in `X-API-Key` is rejected by the
//                 gateway (that header is for real API keys only) and used
//                 to trigger a logout loop.
//
// `authMode` is the discriminator. `apiKey` is only meaningful when
// `authMode === "apikey"`. `apiClient.authHeaders()` branches on this.

export type AuthMode = "apikey" | "session" | "anonymous";

export type AuthCredentials =
  | { mode: "apikey"; key: string }
  | { mode: "session" };

// ---------------------------------------------------------------------------
// Derive auth fields from a config patch (legacy embedded-apiKey support)
// ---------------------------------------------------------------------------
//
// `update({apiKey: "..."})` is the embedded-mode escape hatch — runtime-config
// (`/config.json`) can pre-populate the dashboard with an API key for
// self-hosted deployments that ship without a login UI. When that path is
// taken, we MUST also flip authMode to "apikey" so apiClient sends the
// X-API-Key header. Clearing the key (`apiKey: ""`) reverts to anonymous.
//
// Patches that don't touch apiKey leave authMode and isAuthenticated alone.

function deriveAuthFromPatch(
  prev: { authMode: AuthMode; isAuthenticated: boolean; apiKey: string },
  patch: { apiKey?: string },
): { authMode: AuthMode; isAuthenticated: boolean } {
  if (patch.apiKey === undefined) {
    return { authMode: prev.authMode, isAuthenticated: prev.isAuthenticated };
  }
  if (patch.apiKey === "") {
    return { authMode: "anonymous", isAuthenticated: false };
  }
  return { authMode: "apikey", isAuthenticated: true };
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
  authMode: AuthMode;
  isAuthenticated: boolean;
  isLoggingOut: boolean;
  loginTimestamp: number | null;
  /** @internal Prevents tenant impersonation via store mutation after login. */
  tenantLocked: boolean;

  /** True once runtime config has been loaded (from public/config.json or defaults). */
  loaded: boolean;

  // Actions
  update: (patch: ConfigPatch) => void;
  login: (creds: AuthCredentials, user: User) => void;
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
    // savedUser implies a prior session-mode login (cookie is the auth artefact;
    // no token is persisted). Treat the restored state as session-authenticated.
    authMode: savedUser ? ("session" as AuthMode) : ("anonymous" as AuthMode),
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
          return { ...next, ...deriveAuthFromPatch(s, safePatch) };
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
        return { ...next, ...deriveAuthFromPatch(s, patch), tenantLocked: locked };
      }),

    login: (creds, user) => {
      logger.info("config-store", "Login", {
        userId: user.id,
        tenant: user.tenant,
        mode: creds.mode,
      });
      const now = Date.now();
      // API key (apikey mode) stays in memory only — cookie auth handles
      // session-mode persistence; nothing is written to localStorage either way.
      clearLegacyToken();
      persistUser(user);
      persistLoginTimestamp(now);
      set({
        // apiKey is ONLY set in apikey mode. Session mode relies on the
        // httpOnly cookie the gateway already set during /auth/login. Storing
        // the session token here would cause apiClient.authHeaders() to send
        // it as X-API-Key, which the gateway rejects (header is for real
        // API keys only) — triggering an immediate logout loop.
        apiKey: creds.mode === "apikey" ? creds.key : "",
        authMode: creds.mode,
        user,
        isAuthenticated: true,
        isLoggingOut: false,
        loginTimestamp: now,
        tenantId: user.tenant ?? "",
        principalId: user.id ?? "",
        principalRole: user.roles?.[0] ?? "",
        tenantLocked: !!(user.tenant),
      });
      broadcastSync({ type: "auth-login", creds, user });
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
      // Per-user persisted "Last verified at" timestamp — drop it so
      // the next operator on this browser starts clean rather than
      // inheriting the previous principal's chain-check snapshot.
      clearVerificationState();
      // Clear React Query cache to prevent cross-tenant/cross-user data leakage
      _queryClient?.clear();
      set({
        apiKey: "",
        authMode: "anonymous",
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
