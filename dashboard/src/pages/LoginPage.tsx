import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Button } from "../components/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../components/ui/Card";
import { Input } from "../components/ui/Input";
import { useAuthConfig } from "../hooks/useAuthConfig";
import { api } from "../lib/api";
import { useConfigStore } from "../state/config";

function resolveBaseUrl(apiBaseUrl: string) {
  if (!apiBaseUrl) {
    return window.location.origin;
  }
  if (apiBaseUrl.startsWith("http://") || apiBaseUrl.startsWith("https://")) {
    return apiBaseUrl.replace(/\/$/, "");
  }
  return `${window.location.origin}${apiBaseUrl.startsWith("/") ? "" : "/"}${apiBaseUrl}`;
}

export function LoginPage() {
  const navigate = useNavigate();
  const { data: authConfig } = useAuthConfig();
  const apiBaseUrl = useConfigStore((state) => state.apiBaseUrl);
  const tenantId = useConfigStore((state) => state.tenantId);
  const updateConfig = useConfigStore((state) => state.update);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const canUsePassword = authConfig?.password_enabled ?? false;
  const canUseSaml = authConfig?.saml_enabled ?? false;
  const samlLoginUrl = authConfig?.saml_login_url ?? "/api/v1/auth/sso/saml/login";
  const redirectUrl = useMemo(() => `${window.location.origin}/auth/callback`, []);

  const handlePasswordLogin = async (event: React.FormEvent) => {
    event.preventDefault();
    setError(null);
    setLoading(true);
    try {
      const res = await api.login({ username, password, tenant: tenantId || undefined });
      updateConfig({
        apiKey: res.token,
        tenantId: res.user.tenant || tenantId,
        principalId: res.user.id,
        principalRole: res.user.roles?.[0] || "",
      });
      navigate("/");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Login failed");
    } finally {
      setLoading(false);
    }
  };

  const handleSamlLogin = () => {
    const base = resolveBaseUrl(apiBaseUrl);
    const url = new URL(samlLoginUrl, base);
    url.searchParams.set("redirect", redirectUrl);
    window.location.assign(url.toString());
  };

  return (
    <div className="min-h-screen bg-[color:var(--surface-muted)]">
      <div className="mx-auto flex min-h-screen w-full max-w-4xl flex-col items-center justify-center gap-8 px-6 py-12">
        <div className="text-center">
          <div className="text-xs uppercase tracking-[0.4em] text-muted">Cordum</div>
          <h1 className="font-display text-3xl font-semibold text-ink">Enterprise Console</h1>
          <p className="mt-2 text-sm text-muted">Sign in to manage workflows, packs, and policy controls.</p>
        </div>
        <div className="grid w-full gap-6 md:grid-cols-2">
          <Card className="space-y-4">
            <CardHeader>
              <CardTitle>Password Login</CardTitle>
            </CardHeader>
            <CardDescription>Use your enterprise credentials to access the control plane.</CardDescription>
            <form className="space-y-3" onSubmit={handlePasswordLogin}>
              <Input
                type="text"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                placeholder="Username or email"
                disabled={!canUsePassword || loading}
              />
              <Input
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                placeholder="Password"
                disabled={!canUsePassword || loading}
              />
              {error ? <div className="text-xs text-danger">{error}</div> : null}
              <Button type="submit" variant="primary" disabled={!canUsePassword || loading}>
                {loading ? "Signing in..." : "Sign in"}
              </Button>
              {!canUsePassword ? <div className="text-xs text-muted">Password login is disabled.</div> : null}
            </form>
          </Card>
          <Card className="space-y-4">
            <CardHeader>
              <CardTitle>Single Sign-On</CardTitle>
            </CardHeader>
            <CardDescription>Use SSO to authenticate with your identity provider.</CardDescription>
            <Button type="button" variant="outline" disabled={!canUseSaml} onClick={handleSamlLogin}>
              Continue with SSO
            </Button>
            {!canUseSaml ? <div className="text-xs text-muted">SAML SSO is not configured.</div> : null}
          </Card>
        </div>
      </div>
    </div>
  );
}
