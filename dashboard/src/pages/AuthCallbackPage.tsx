import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useConfigStore } from "../state/config";
import { api } from "../lib/api";
import type { User } from "../api/types";

function parseHashParams() {
  const hash = window.location.hash.replace(/^#/, "");
  return new URLSearchParams(hash);
}

function clearCallbackHash() {
  if (typeof window !== "undefined") {
    window.history.replaceState(null, "", window.location.pathname);
  }
}

export interface AuthCallbackDeps {
  fetchSession: () => Promise<{ user: User }>;
  login: (token: string, user: User) => void;
}

export function buildFallbackCallbackUser({
  tenant,
  principalId,
  role,
}: {
  tenant: string | null;
  principalId: string | null;
  role: string | null;
}): User {
  const fallbackId = principalId?.trim() || "oidc-user";
  const fallbackTenant = tenant?.trim() || "";
  const fallbackRole = role?.trim();

  return {
    id: fallbackId,
    username: fallbackId,
    email: `${fallbackId}@local.invalid`,
    display_name: fallbackId,
    roles: fallbackRole ? [fallbackRole] : [],
    tenant: fallbackTenant,
  };
}

export async function completeAuthCallback(
  params: URLSearchParams,
  { fetchSession, login }: AuthCallbackDeps,
): Promise<{ ok: true } | { ok: false; error: string }> {
  const token = params.get("token")?.trim();
  const tenant = params.get("tenant");
  const principalId = params.get("principal_id");
  const role = params.get("role");
  if (!token) {
    return { ok: false, error: "Missing session token." };
  }

  try {
    const session = await fetchSession();
    login(token, session.user);
  } catch {
    login(token, buildFallbackCallbackUser({ tenant, principalId, role }));
  }

  return { ok: true };
}

export default function AuthCallbackPage() {
  const navigate = useNavigate();
  const login = useConfigStore((state) => state.login);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let mounted = true;
    let shouldNavigate = false;
    const params = parseHashParams();

    completeAuthCallback(params, { fetchSession: api.getSession, login })
      .then((result) => {
        if (!mounted) return;
        if (!result.ok) {
          setError(result.error);
          return;
        }
        shouldNavigate = true;
      })
      .finally(() => {
        clearCallbackHash();
        if (mounted && shouldNavigate) {
          navigate("/", { replace: true });
        }
      });

    return () => {
      mounted = false;
    };
  }, [navigate, login]);

  return (
    <div className="min-h-screen bg-[color:var(--surface-muted)]">
      <div className="mx-auto flex min-h-screen w-full max-w-xl flex-col items-center justify-center gap-4 px-6 py-12 text-center">
        <div className="text-xs uppercase tracking-[0.4em] text-muted-foreground">Cordum</div>
        <h1 className="font-display text-2xl font-semibold text-ink">Completing sign-in…</h1>
        <p className="text-sm text-muted-foreground">Preparing your console session.</p>
        {error ? <div className="text-xs text-danger">{error}</div> : null}
      </div>
    </div>
  );
}
