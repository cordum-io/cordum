import { useEffect, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { useConfigStore } from "../state/config";
import { useToastStore } from "../state/toast";
import { AppShell } from "./layout/AppShell";
import { CommandPalette } from "./CommandPalette";
import { get } from "../api/client";
import { ApiError } from "../api/client";
import { useEventStream } from "../hooks/useEventStream";
import { useKeyboardShortcuts } from "../hooks/useKeyboardShortcuts";
import { useAuthConfig } from "../hooks/useAuthConfig";
import { useCrossTabSync } from "../hooks/useCrossTabSync";
import { KeyboardShortcutsHelp } from "./KeyboardShortcutsHelp";
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
  const requiresAuth = !!authConfig && (
    authConfig.password_enabled ||
    authConfig.user_auth_enabled ||
    authConfig.saml_enabled
  );
  const isAuthorized = !requiresAuth || isAuthenticated;

  // Redirect to login if not authenticated
  useEffect(() => {
    if (!authLoading && !isAuthorized) {
      const returnUrl = location.pathname + location.search;
      navigate(`/login?returnUrl=${encodeURIComponent(returnUrl)}`, { replace: true });
    }
  }, [authLoading, isAuthorized, navigate, location.pathname, location.search]);

  // Validate session on mount
  const sessionQuery = useQuery({
    queryKey: ["auth-session-validate"],
    queryFn: () => get<SessionResponse>("/auth/session"),
    enabled: requiresAuth && isAuthenticated,
    retry: false,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  });

  // Handle 401 from session validation
  const addToast = useToastStore((s) => s.addToast);
  useEffect(() => {
    if (sessionQuery.error instanceof ApiError && sessionQuery.error.status === 401) {
      addToast({
        type: "warning",
        title: "Session expired",
        description: "Please sign in again to continue.",
        duration: 5000,
      });
      logout();
    }
  }, [sessionQuery.error, logout, addToast]);

  // Connect WebSocket when authenticated (disconnects on unmount / logout)
  useEventStream();

  // Register global keyboard shortcuts
  useKeyboardShortcuts();

  // Sync auth & theme across browser tabs
  useCrossTabSync();

  if (!isAuthorized) {
    return null;
  }

  return (
    <>
      <SessionTimeoutWarning />
      <AppShell>{children}</AppShell>
      <CommandPalette />
      <KeyboardShortcutsHelp />
    </>
  );
}
