/*
 * DESIGN: "Control Surface" — Login
 * Multi-auth: API Key, Password, OIDC, SAML
 */
import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { motion, AnimatePresence } from "framer-motion";
import { useConfigStore } from "@/state/config";
import { Button } from "@/components/ui/Button";
import type { User } from "@/api/types";
import { toast } from "sonner";
import {
  KeyRound,
  ArrowRight,
  Layers,
  Lock,
  Globe,
  Building2,
  ChevronDown,
} from "lucide-react";
import { cn } from "@/lib/utils";

type AuthMode = "api_key" | "password" | "oidc" | "saml";

const LOGIN_TIMEOUT = 10_000;

/** Validate returnUrl is a safe relative path — blocks open redirect attacks. */
export function isSafeReturnUrl(url: string | null): string {
  if (!url || typeof url !== "string") return "/";
  const trimmed = url.trim();
  if (!trimmed.startsWith("/")) return "/";
  if (trimmed.startsWith("//")) return "/";
  if (/[:\s]/.test(trimmed)) return "/";
  try {
    const parsed = new URL(trimmed, "http://localhost");
    if (parsed.origin !== "http://localhost") return "/";
    if (parsed.protocol !== "http:") return "/";
  } catch {
    return "/";
  }
  return trimmed;
}

const authModes: { id: AuthMode; label: string; icon: React.ReactNode; description: string }[] = [
  { id: "api_key", label: "API Key", icon: <KeyRound className="w-4 h-4" />, description: "Connect with an API key" },
  { id: "password", label: "Password", icon: <Lock className="w-4 h-4" />, description: "Username & password login" },
  { id: "oidc", label: "OIDC / SSO", icon: <Globe className="w-4 h-4" />, description: "OpenID Connect provider" },
  { id: "saml", label: "SAML / Enterprise", icon: <Building2 className="w-4 h-4" />, description: "Enterprise SAML SSO" },
];

export default function LoginPage() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const login = useConfigStore((s) => s.login);
  const returnUrl = isSafeReturnUrl(searchParams.get("returnUrl"));
  const [authMode, setAuthMode] = useState<AuthMode>("api_key");
  const [showModeSelector, setShowModeSelector] = useState(false);

  // API Key fields
  const [apiUrl, setApiUrl] = useState("");
  const [apiKey, setApiKey] = useState("");

  // Password fields
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  const [loading, setLoading] = useState(false);
  const successToastClass =
    "border border-[color:var(--color-success)]/30 bg-card text-[var(--color-success)]";
  const errorToastClass =
    "border border-destructive/30 bg-card text-destructive";
  const showSuccessToast = (message: string) =>
    toast.success(message, { className: successToastClass });
  const showErrorToast = (message: string) =>
    toast.error(message, { className: errorToastClass });

  const handleApiKeyLogin = async () => {
    if (!apiKey.trim()) {
      showErrorToast("API key is required");
      return;
    }
    setLoading(true);
    try {
      const raw = apiUrl.trim();
      const baseUrl = isSafeApiUrl(raw);
      if (raw && baseUrl !== raw) {
        toast.warning("Unsafe API URL blocked — using default endpoint");
      }
      const res = await fetch(`${baseUrl}/auth/me`, {
        headers: { Authorization: `Bearer ${apiKey.trim()}` },
        signal: AbortSignal.timeout(LOGIN_TIMEOUT),
      });
      if (res.ok) {
        const user = await res.json();
        login(apiKey.trim(), user);
        toast.success("Connected to Cordum");
        navigate(returnUrl);
      } else {
        const msg = res.status === 401 || res.status === 403
          ? "Invalid API key"
          : res.status >= 500
            ? "Server error — try again later"
            : `Connection failed (HTTP ${res.status})`;
        toast.error(msg);
      }
    } catch (err) {
      if (err instanceof DOMException && err.name === "TimeoutError") {
        toast.error("Request timed out — check your connection");
      } else {
        toast.error("Cannot reach API server — check the endpoint URL");
      }
    } finally {
      setLoading(false);
    }
  };

  const handlePasswordLogin = async () => {
    if (!username.trim() || !password.trim()) {
      showErrorToast("Username and password are required");
      return;
    }
    setLoading(true);
    try {
      const raw = apiUrl.trim();
      const baseUrl = isSafeApiUrl(raw);
      if (raw && baseUrl !== raw) {
        toast.warning("Unsafe API URL blocked — using default endpoint");
      }
      const res = await fetch(`${baseUrl}/auth/login`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username: username.trim(), password: password.trim() }),
        signal: AbortSignal.timeout(LOGIN_TIMEOUT),
      });
      if (res.ok) {
        const data = await res.json();
        // Fallback user object for servers that return { token } without a user field.
        // Roles default to ["admin"] — the backend enforces real RBAC via the token.
        login(data.token || "session", data.user || {
          id: username.trim(),
          username: username.trim(),
          email: "",
          display_name: username.trim(),
          roles: ["admin"],
          tenant: "default",
        });
        toast.success("Logged in");
        navigate(returnUrl);
      } else {
        toast.error("Invalid credentials");
      }
    } catch (err) {
      if (err instanceof DOMException && err.name === "TimeoutError") {
        toast.error("Request timed out — check your connection");
      } else {
        toast.error("Cannot reach API server — check the endpoint URL");
      }
    } finally {
      setLoading(false);
    }
  };

  const handleOidcLogin = () => {
    const raw = apiUrl.trim();
    const baseUrl = isSafeApiUrl(raw);
    if (raw && baseUrl !== raw) {
      toast.warning("Unsafe API URL blocked — using default endpoint");
    }
    toast.info("Redirecting to OIDC provider...");
    window.location.href = `${baseUrl}/auth/oidc/login`;
  };

  const handleSamlLogin = () => {
    const raw = apiUrl.trim();
    const baseUrl = isSafeApiUrl(raw);
    if (raw && baseUrl !== raw) {
      toast.warning("Unsafe API URL blocked — using default endpoint");
    }
    toast.info("Redirecting to SAML IdP...");
    window.location.href = `${baseUrl}/auth/saml/login`;
  };

  const handleSubmit = () => {
    switch (authMode) {
      case "api_key":
        return handleApiKeyLogin();
      case "password":
        return handlePasswordLogin();
      case "oidc":
        return handleOidcLogin();
      case "saml":
        return handleSamlLogin();
    }
  };

  const currentMode = authModes.find((m) => m.id === authMode)!;

  return (
    <div className="relative flex min-h-screen items-center justify-center overflow-hidden bg-[radial-gradient(900px_circle_at_10%_-10%,var(--bg-radial-1),transparent_55%),radial-gradient(700px_circle_at_90%_0%,var(--bg-radial-2),transparent_45%),linear-gradient(120deg,var(--bg-linear-1)_0%,var(--bg-linear-2)_55%,var(--bg-linear-3)_100%)] px-4 font-sans">
      {/* Ambient warm glows */}
      <div className="pointer-events-none absolute -left-20 top-0 h-72 w-72 rounded-full bg-[color:var(--bg-radial-1)] blur-3xl" />
      <div className="pointer-events-none absolute -right-20 bottom-0 h-72 w-72 rounded-full bg-[color:var(--bg-radial-2)] blur-3xl" />
      <div className="pointer-events-none absolute top-1/2 left-1/2 h-[600px] w-[600px] -translate-x-1/2 -translate-y-1/2 rounded-full bg-primary/10 blur-[120px]" />

      <motion.div
        initial={{ opacity: 0, y: 20 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.5, ease: "easeOut" }}
        className="w-full max-w-sm space-y-8 relative z-10"
      >
        {/* Logo */}
        <div className="flex flex-col items-center">
          <div className="w-14 h-14 rounded-xl bg-cordum/10 border border-cordum/20 flex items-center justify-center mb-4 glow-cordum">
            <Layers className="w-7 h-7 text-cordum" />
          </div>
          <h1 className="text-2xl font-bold font-display text-foreground tracking-tight">
            Cordum
          </h1>
          <p className="text-xs font-mono text-muted-foreground mt-1 uppercase tracking-[0.15em]">
            Agent Control Plane
          </p>
        </div>

        {/* Form — Mac glass card style */}
        <div className="surface-card space-y-5 rounded-3xl border border-border bg-[color:var(--surface-glass)] p-6 shadow-glow backdrop-blur-xl">
          {/* Auth Mode Selector */}
          <div className="relative">
            <button
              type="button"
              onClick={() => setShowModeSelector(!showModeSelector)}
              className="w-full flex items-center justify-between h-9 px-3 text-sm bg-surface-0 border border-border rounded-2xl text-foreground hover:bg-surface-1 transition-colors"
            >
              <div className="flex items-center gap-2">
                <span className="text-muted-foreground">
                  {currentMode.icon}
                </span>
                <span className="font-medium">{currentMode.label}</span>
              </div>
              <ChevronDown
                className={cn(
                  "w-3.5 h-3.5 text-muted-foreground transition-transform",
                  showModeSelector && "rotate-180",
                )}
              />
            </button>

            <AnimatePresence>
              {showModeSelector && (
                <motion.div
                  initial={{ opacity: 0, y: -4 }}
                  animate={{ opacity: 1, y: 0 }}
                  exit={{ opacity: 0, y: -4 }}
                  className="absolute top-full left-0 right-0 mt-1 bg-surface-1 border border-border rounded-2xl shadow-xl z-20 overflow-hidden"
                >
                  {authModes.map((mode) => (
                    <button
                      type="button"
                      key={mode.id}
                      onClick={() => {
                        setAuthMode(mode.id);
                        setShowModeSelector(false);
                      }}
                      className={cn(
                        "w-full flex items-center gap-3 px-3 py-2.5 text-left hover:bg-surface-2 transition-colors",
                        authMode === mode.id && "bg-cordum/5",
                      )}
                    >
                      <span
                        className={cn(
                          "text-muted-foreground",
                          authMode === mode.id && "text-cordum",
                        )}
                      >
                        {mode.icon}
                      </span>
                      <div>
                        <p
                          className={cn(
                            "text-sm font-medium",
                            authMode === mode.id
                              ? "text-cordum"
                              : "text-foreground",
                          )}
                        >
                          {mode.label}
                        </p>
                        <p className="text-xs text-muted-foreground">
                          {mode.description}
                        </p>
                      </div>
                    </button>
                  ))}
                </motion.div>
              )}
            </AnimatePresence>
          </div>

          {/* API Endpoint — always shown */}
          <div className="space-y-2">
            <label className="text-xs font-mono font-semibold text-muted-foreground uppercase tracking-[0.08em]">
              API Endpoint
            </label>
            <input
              type="text"
              placeholder="/api/v1"
              value={apiUrl}
              onChange={(e) => setApiUrl(e.target.value)}
              className="h-9 w-full rounded-2xl border border-border bg-card/80 px-3 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/30 font-mono"
            />
          </div>

          {/* API Key mode */}
          {authMode === "api_key" && (
            <motion.div
              key="api_key"
              initial={{ opacity: 0, height: 0 }}
              animate={{ opacity: 1, height: "auto" }}
              exit={{ opacity: 0, height: 0 }}
              className="space-y-2"
            >
              <label className="text-xs font-mono font-semibold text-muted-foreground uppercase tracking-[0.08em]">
                API Key
              </label>
              <div className="relative">
                <KeyRound className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
                <input
                  type="password"
                  placeholder="Enter your API key"
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && handleSubmit()}
                  className="h-9 w-full rounded-2xl border border-border bg-card/80 pl-9 pr-3 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/30 font-mono"
                />
              </div>
            )}
            <div>
              <label htmlFor="username" className="mb-1.5 block text-xs font-semibold uppercase tracking-wide text-muted">
                Username
              </label>
              <Input
                id="username"
                name="username"
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
                name="password"
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
                name="tenant"
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
                name="api-key"
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
                name="tenant-api"
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
