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
  const navigate = useNavigate();
  const location = useLocation();

  useEffect(() => {
    if (!isAuthenticated) {
      const returnUrl = location.pathname + location.search;
      navigate(`/login?returnUrl=${encodeURIComponent(returnUrl)}`, { replace: true });
    }
  }, [isAuthenticated, navigate, location.pathname, location.search]);

  return isAuthenticated;
}
