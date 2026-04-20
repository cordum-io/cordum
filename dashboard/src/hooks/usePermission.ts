import { useConfigStore } from "../state/config";
import { useAuthConfig } from "./useAuthConfig";

/**
 * Client-side display gating only — the backend enforces real authorization.
 * Returns allowed=true when no auth is configured (graceful degradation).
 */
export function usePermission(requiredRoles: string[]): {
  allowed: boolean;
  userRoles: string[];
} {
  const { data: authConfig } = useAuthConfig();
  const user = useConfigStore((s) => s.user);
  const principalRole = useConfigStore((s) => s.principalRole);

  const requiresAuth =
    !!authConfig &&
    (authConfig.password_enabled ||
      !!authConfig.user_auth_enabled ||
      authConfig.saml_enabled ||
      authConfig.oidc_enabled);

  // Graceful degradation: if no auth configured, allow everything
  if (!requiresAuth) return { allowed: true, userRoles: [] };

  const userRoles = user?.roles ?? [];
  const hasRole = requiredRoles.some(
    (r) => userRoles.includes(r) || principalRole === r,
  );

  return { allowed: hasRole, userRoles };
}

export function useIsAdmin(): boolean {
  const { allowed } = usePermission(["admin"]);
  return allowed;
}

