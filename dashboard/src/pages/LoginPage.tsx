import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Badge } from "../components/ui/Badge";
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
  const canUseUserAuth = authConfig?.user_auth_enabled ?? false;
  const canUseSaml = authConfig?.saml_enabled ?? false;
  const isSamlEnterprise = authConfig?.saml_enterprise ?? true;
  const samlLoginUrl = authConfig?.saml_login_url ?? "/api/v1/auth/sso/saml/login";
  const redirectUrl = useMemo(() => `${window.location.origin}/auth/callback`, []);

  // Determine if any password-based auth is available
  const canLogin = canUsePassword || canUseUserAuth;

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
      <div className="mx-auto flex min-h-screen w-full max-w-md flex-col items-center justify-center gap-8 px-6 py-12">
        <div className="text-center">
          <div className="text-xs uppercase tracking-[0.4em] text-muted">Cordum</div>
          <h1 className="font-display text-3xl font-semibold text-ink">Enterprise Console</h1>
          <p className="mt-2 text-sm text-muted">Sign in to manage workflows, packs, and policy controls.</p>
        </div>

        <Card className="w-full space-y-6 p-6">
          <CardHeader className="p-0">
            <CardTitle>Sign In</CardTitle>
            <CardDescription>Enter your credentials to access the control plane.</CardDescription>
          </CardHeader>

          <form className="space-y-4" onSubmit={handlePasswordLogin}>
            <div className="space-y-2">
              <label htmlFor="username" className="text-sm font-medium text-ink">
                Email or username
              </label>
              <Input
                id="username"
                type="text"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                placeholder="Enter your email or username"
                disabled={!canLogin || loading}
                autoComplete="username"
              />
            </div>
            <div className="space-y-2">
              <label htmlFor="password" className="text-sm font-medium text-ink">
                Password
              </label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                placeholder="Enter your password"
                disabled={!canLogin || loading}
                autoComplete="current-password"
              />
            </div>

            {error ? <div className="text-xs text-danger">{error}</div> : null}

            <Button type="submit" variant="primary" className="w-full" disabled={!canLogin || loading}>
              {loading ? "Signing in..." : "Sign In"}
            </Button>

            {!canLogin ? (
              <div className="text-center text-xs text-muted">
                Password login is disabled. Please use SSO or contact your administrator.
              </div>
            ) : null}
          </form>

          {canUseSaml ? (
            <>
              <div className="relative">
                <div className="absolute inset-0 flex items-center">
                  <span className="w-full border-t border-surface2" />
                </div>
                <div className="relative flex justify-center text-xs uppercase">
                  <span className="bg-surface px-2 text-muted">or</span>
                </div>
              </div>

              <div className="space-y-3">
                <Button
                  type="button"
                  variant="outline"
                  className="w-full"
                  onClick={handleSamlLogin}
                >
                  <span className="flex items-center justify-center gap-2">
                    Continue with SSO
                    {isSamlEnterprise ? <Badge variant="enterprise">Enterprise</Badge> : null}
                  </span>
                </Button>
              </div>
            </>
          ) : (
            <div className="text-center text-xs text-muted">
              SSO is not configured.{" "}
              {isSamlEnterprise ? (
                <span className="inline-flex items-center gap-1">
                  <Badge variant="enterprise">Enterprise</Badge>
                </span>
              ) : null}
            </div>
          )}
        </Card>

        <p className="text-center text-xs text-muted">
          By signing in, you agree to the terms of service and privacy policy.
        </p>
      </div>
    </div>
  );
}
