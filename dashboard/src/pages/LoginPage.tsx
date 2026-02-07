import { useEffect, useState, type FormEvent } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { Card } from "../components/ui/Card";
import { useAuthConfig } from "../hooks/useAuthConfig";
import { useConfigStore } from "../state/config";
import { post } from "../api/client";
import type { User } from "../api/types";

interface LoginResponse {
  token: string;
  user: User;
}

export default function LoginPage() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const returnUrl = searchParams.get("returnUrl") || "/";
  const { data: authConfig, isLoading: authLoading } = useAuthConfig();
  const login = useConfigStore((s) => s.login);

  const userAuthEnabled = authConfig?.user_auth_enabled ?? false;
  const apiKeyEnabled = authConfig?.password_enabled ?? false;
  const defaultTenant = authConfig?.default_tenant || "default";
  const authRequired = userAuthEnabled || apiKeyEnabled || authConfig?.saml_enabled;

  type LoginMode = "user" | "apiKey";
  const [mode, setMode] = useState<LoginMode>(userAuthEnabled ? "user" : "apiKey");

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [apiKeyInput, setApiKeyInput] = useState("");
  const [tenantInput, setTenantInput] = useState(defaultTenant);
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const showModeToggle = userAuthEnabled && apiKeyEnabled;
  const effectiveMode: LoginMode = userAuthEnabled ? mode : "apiKey";

  useEffect(() => {
    if (userAuthEnabled) {
      setMode((m) => (m === "apiKey" && !apiKeyEnabled ? "user" : m));
    } else {
      setMode("apiKey");
    }
  }, [userAuthEnabled, apiKeyEnabled]);

  useEffect(() => {
    setTenantInput((t) => t || defaultTenant);
  }, [defaultTenant]);

  const handlePasswordLogin = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setSubmitting(true);
    try {
      const tenant = tenantInput.trim();
      const res = await post<LoginResponse>("/auth/login", {
        username,
        password,
        tenant: tenant || undefined,
      });
      login(res.token, res.user);
      navigate(returnUrl, { replace: true });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Login failed");
    } finally {
      setSubmitting(false);
    }
  };

  const handleApiKeyLogin = (e: FormEvent) => {
    e.preventDefault();
    setError("");
    const trimmed = apiKeyInput.trim();
    if (!trimmed) {
      setError("API key is required");
      return;
    }
    const tenant = tenantInput.trim() || defaultTenant;
    login(trimmed, {
      id: "",
      username: "api-key-user",
      email: "",
      display_name: "API Key User",
      roles: [],
      tenant,
    });
    navigate(returnUrl, { replace: true });
  };

  return (
    <div className="flex min-h-screen items-center justify-center px-4">
      <Card className="w-full max-w-md p-8">
        <div className="mb-8 text-center">
          <img
            src="/assets/cordum-logo.png"
            alt="Cordum logo"
            className="mx-auto mb-4 h-12 w-auto object-contain dark:brightness-0 dark:invert"
          />
          <h1 className="font-display text-2xl font-semibold text-ink">
            Cordum Control Plane
          </h1>
          <p className="mt-1 text-sm text-muted">
            Sign in to continue
          </p>
        </div>

        {authLoading ? (
          <div className="py-8 text-center text-sm text-muted">
            Loading auth configuration...
          </div>
        ) : !authRequired ? (
          <div className="space-y-4 text-center">
            <p className="text-sm text-muted">
              Authentication is not required for this deployment.
            </p>
            <Button type="button" className="w-full" onClick={() => navigate(returnUrl, { replace: true })}>
              Continue
            </Button>
          </div>
        ) : effectiveMode === "user" ? (
          <form onSubmit={handlePasswordLogin} className="space-y-4">
            {showModeToggle && (
              <div className="flex rounded-full border border-border p-1">
                <button
                  type="button"
                  className={`flex-1 rounded-full px-3 py-1.5 text-xs font-semibold uppercase tracking-wide ${
                    mode === "user" ? "bg-accent/15 text-accent" : "text-muted"
                  }`}
                  onClick={() => setMode("user")}
                >
                  User
                </button>
                <button
                  type="button"
                  className={`flex-1 rounded-full px-3 py-1.5 text-xs font-semibold uppercase tracking-wide ${
                    mode === "apiKey" ? "bg-accent/15 text-accent" : "text-muted"
                  }`}
                  onClick={() => setMode("apiKey")}
                >
                  API Key
                </button>
              </div>
            )}
            <div>
              <label htmlFor="username" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted">
                Username
              </label>
              <Input
                id="username"
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="Enter username"
                autoComplete="username"
                required
              />
            </div>
            <div>
              <label htmlFor="password" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted">
                Password
              </label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="Enter password"
                autoComplete="current-password"
                required
              />
            </div>
            <div>
              <label htmlFor="tenant" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted">
                Tenant
              </label>
              <Input
                id="tenant"
                type="text"
                value={tenantInput}
                onChange={(e) => setTenantInput(e.target.value)}
                placeholder={defaultTenant}
                autoComplete="organization"
              />
            </div>
            {error && (
              <div className="rounded-xl bg-[color:rgba(184,58,58,0.1)] px-4 py-2.5 text-sm text-danger">
                {error}
              </div>
            )}
            <Button type="submit" className="w-full" disabled={submitting}>
              {submitting ? "Signing in..." : "Sign in"}
            </Button>
          </form>
        ) : (
          <form onSubmit={handleApiKeyLogin} className="space-y-4">
            {showModeToggle && (
              <div className="flex rounded-full border border-border p-1">
                <button
                  type="button"
                  className={`flex-1 rounded-full px-3 py-1.5 text-xs font-semibold uppercase tracking-wide ${
                    mode === "user" ? "text-muted" : "bg-accent/15 text-accent"
                  }`}
                  onClick={() => setMode("user")}
                >
                  User
                </button>
                <button
                  type="button"
                  className={`flex-1 rounded-full px-3 py-1.5 text-xs font-semibold uppercase tracking-wide ${
                    mode === "apiKey" ? "bg-accent/15 text-accent" : "text-muted"
                  }`}
                  onClick={() => setMode("apiKey")}
                >
                  API Key
                </button>
              </div>
            )}
            <div>
              <label htmlFor="api-key" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted">
                API Key
              </label>
              <Input
                id="api-key"
                type="password"
                value={apiKeyInput}
                onChange={(e) => setApiKeyInput(e.target.value)}
                placeholder="Enter your API key"
                autoComplete="off"
                required
              />
            </div>
            <div>
              <label htmlFor="tenant-api" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted">
                Tenant
              </label>
              <Input
                id="tenant-api"
                type="text"
                value={tenantInput}
                onChange={(e) => setTenantInput(e.target.value)}
                placeholder={defaultTenant}
                autoComplete="organization"
              />
            </div>
            {error && (
              <div className="rounded-xl bg-[color:rgba(184,58,58,0.1)] px-4 py-2.5 text-sm text-danger">
                {error}
              </div>
            )}
            <Button type="submit" className="w-full" disabled={submitting}>
              {submitting ? "Connecting..." : "Connect"}
            </Button>
            <p className="text-center text-xs text-muted">
              API-key authentication mode
            </p>
          </form>
        )}
      </Card>
    </div>
  );
}
