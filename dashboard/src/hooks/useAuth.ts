import { useEffect } from "react";
import { useNavigate, useLocation } from "react-router-dom";
import { useConfigStore } from "../state/config";

export function useAuth() {
  const token = useConfigStore((s) => s.apiKey);
  const user = useConfigStore((s) => s.user);
  const isAuthenticated = useConfigStore((s) => s.isAuthenticated);
  const login = useConfigStore((s) => s.login);
  const logout = useConfigStore((s) => s.logout);
  const tenantId = useConfigStore((s) => s.tenantId);
  const principalId = useConfigStore((s) => s.principalId);

  return { token, user, isAuthenticated, login, logout, tenantId, principalId };
}

export function useRequireAuth() {
  const isAuthenticated = useConfigStore((s) => s.isAuthenticated);
  const user = useConfigStore((s) => s.user);
  const navigate = useNavigate();
  const location = useLocation();

  // Strict gate: refuse to render the dashboard unless an explicit login()
  // has populated `user`. An embedded API key in /config.json sets
  // isAuthenticated=true via update(), but does NOT identify a principal —
  // we don't know which role the operator has, so admin gates would silently
  // 403 every action. Force the operator through /login so the backend's
  // /auth/login response hydrates the user (and roles) properly.
  const hasVerifiedPrincipal = isAuthenticated && !!user;

  useEffect(() => {
    if (!hasVerifiedPrincipal) {
      const returnUrl = location.pathname + location.search;
      navigate(`/login?returnUrl=${encodeURIComponent(returnUrl)}`, { replace: true });
    }
  }, [hasVerifiedPrincipal, navigate, location.pathname, location.search]);

  return hasVerifiedPrincipal;
}
