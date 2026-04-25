import { useConfigStore } from "../state/config";
import { useAuthConfig } from "./useAuthConfig";

/**
 * Client-side display gating only — the backend enforces real authorization.
 *
 * Three states the auth config can be in, treated distinctly:
 *  1. Loading or fetch error (`authConfig` undefined) → deny. We don't know
 *     whether auth is required, so don't paint admin affordances we'd then
 *     have to retract; UX > information-disclosure here.
 *  2. Loaded with every auth mode disabled → allow (intentional graceful
 *     degradation for no-auth dev / single-user deployments).
 *  3. Loaded with at least one auth mode enabled → real role check.
 */
export function usePermission(requiredRoles: string[]): {
  allowed: boolean;
  userRoles: string[];
} {
  const { data: authConfig } = useAuthConfig();
  const user = useConfigStore((s) => s.user);
  const principalRole = useConfigStore((s) => s.principalRole);

  // (1) Auth config not yet loaded (or load failed) — fail closed.
  if (!authConfig) {
    return { allowed: false, userRoles: user?.roles ?? [] };
  }

  // (2) Auth config loaded but every mode is off — no-auth deployment.
  const requiresAuth =
    authConfig.password_enabled ||
    !!authConfig.user_auth_enabled ||
    authConfig.saml_enabled ||
    authConfig.oidc_enabled;
  if (!requiresAuth) return { allowed: true, userRoles: [] };

  // (3) Real role check.
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

