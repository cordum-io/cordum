import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useConfigStore } from "../state/config";
import { api } from "../lib/api";

function parseHashParams() {
  const hash = window.location.hash.replace(/^#/, "");
  return new URLSearchParams(hash);
}

export function AuthCallbackPage() {
  const navigate = useNavigate();
  const updateConfig = useConfigStore((state) => state.update);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const params = parseHashParams();
    const token = params.get("token");
    const tenant = params.get("tenant");
    const principalId = params.get("principal_id");
    const role = params.get("role");
    if (!token) {
      setError("Missing session token.");
      return;
    }
    updateConfig({
      apiKey: token,
      tenantId: tenant || undefined,
      principalId: principalId || undefined,
      principalRole: role || undefined,
    });

    api
      .getSession()
      .then((session) => {
        updateConfig({
          tenantId: session.user.tenant,
          principalId: session.user.id,
          principalRole: session.user.roles?.[0] || "",
        });
      })
      .catch(() => {
        // Ignore session fetch errors; token is already stored.
      })
      .finally(() => {
        navigate("/");
      });
  }, [navigate, updateConfig]);

  return (
    <div className="min-h-screen bg-[color:var(--surface-muted)]">
      <div className="mx-auto flex min-h-screen w-full max-w-xl flex-col items-center justify-center gap-4 px-6 py-12 text-center">
        <div className="text-xs uppercase tracking-[0.4em] text-muted">Cordum</div>
        <h1 className="font-display text-2xl font-semibold text-ink">Completing sign-in…</h1>
        <p className="text-sm text-muted">Preparing your console session.</p>
        {error ? <div className="text-xs text-danger">{error}</div> : null}
      </div>
    </div>
  );
}
