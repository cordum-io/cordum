import { useEffect, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { useConfigStore } from "../state/config";
import { useToastStore } from "../state/toast";
import { AppShell } from "./layout/AppShell";
import { LoadingScreen } from "./layout/LoadingScreen";
import { CommandPalette } from "./CommandPalette";
import { get } from "../api/client";
import { ApiError } from "../api/client";
import { useAuthConfig } from "../hooks/useAuthConfig";
import { useCrossTabSync } from "../hooks/useCrossTabSync";
import { SessionTimeoutWarning } from "./SessionTimeoutWarning";
import type { User } from "../api/types";

interface SessionResponse {
  user: User;
}

export function ProtectedRoute({ children }: { children: ReactNode }) {
  const isAuthenticated = useConfigStore((s) => s.isAuthenticated);
  const logout = useConfigStore((s) => s.logout);
  const navigate = useNavigate();
  const location = useLocation();
  const { data: authConfig, isLoading: authLoading } = useAuthConfig();
  // Fail-closed: an unresolved auth-config (`authConfig` is undefined while the
  // /auth/config query is loading, and stays undefined if it errors or times out
  // — useAuthConfig is a plain useQuery with no initialData) is treated as
  // "auth required", never "auth disabled". The loading vs. error states are
  // distinguished below: while loading we render a placeholder; once loading is
  // done a missing config keeps requiresAuth=true so an unauthenticated user
  // is redirected to /login instead of mounting the protected shell.
  const requiresAuth =
    !authConfig ||
    authConfig.password_enabled ||
    authConfig.user_auth_enabled ||
    authConfig.saml_enabled ||
    authConfig.oidc_enabled;
  const isAuthorized = !requiresAuth || isAuthenticated;

  // Redirect to login if not authenticated
  useEffect(() => {
    if (!authLoading && !isAuthorized) {
      const returnUrl = location.pathname + location.search;
      navigate(`/login?returnUrl=${encodeURIComponent(returnUrl)}`, {
        replace: true,
      });
    }
  }, [authLoading, isAuthorized, navigate, location.pathname, location.search]);

  // Validate session on mount
  const sessionQuery = useQuery({
    queryKey: ["auth-session-validate"],
    queryFn: () => get<SessionResponse>("/auth/session"),
    // Gate on !authLoading so session validation cannot run (and trip the
    // 401/logout flow) while /auth/config is still resolving — requiresAuth is
    // fail-closed `true` during loading, so without this a persisted
    // isAuthenticated user would be validated/logged out prematurely.
    enabled: !authLoading && requiresAuth && isAuthenticated,
    retry: false,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  });

  // Handle 401 from session validation
  const addToast = useToastStore((s) => s.addToast);
  useEffect(() => {
    if (
      sessionQuery.error instanceof ApiError &&
      sessionQuery.error.status === 401
    ) {
      addToast({
        type: "warning",
        title: "Session expired",
        description: "Please sign in again to continue.",
        duration: 5000,
      });
      logout();
    }
  }, [sessionQuery.error, logout, addToast]);

  // Sync auth & theme across browser tabs
  useCrossTabSync();

  // Auth config still resolving: render a placeholder rather than mounting the
  // protected shell/children prematurely (info disclosure) or redirecting (the
  // redirect effect above stays gated on !authLoading, so it never fires here).
  if (authLoading) {
    return (
      <div role="status" aria-live="polite">
        <LoadingScreen />
      </div>
    );
  }

  if (!isAuthorized) {
    return null;
  }

  return (
    <>
      <SessionTimeoutWarning />
      <AppShell>{children}</AppShell>
      <CommandPalette />
    </>
  );
}
