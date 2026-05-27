/*
 * DESIGN: "Control Surface" — Login
 * Multi-auth: API Key, Password, OIDC, SAML
 */
import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { motion, AnimatePresence } from "framer-motion";
import { useConfigStore } from "@/state/config";
import { useAuthConfig } from "@/hooks/useAuthConfig";
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

/** Build a minimal fallback user when the server returns { token } without user data. */
export function buildPasswordFallbackUser(username: string): {
  id: string;
  username: string;
  email: string;
  display_name: string;
  roles: string[];
  tenant: string;
} {
  const trimmed = username.trim();
  return {
    id: trimmed,
    username: trimmed,
    email: "",
    display_name: trimmed,
    roles: ["viewer"],
    tenant: "default",
  };
}

export async function readJsonIfOk<T>(
  response: Pick<Response, "ok" | "json">,
): Promise<T | null> {
  if (!response.ok) return null;
  try {
    return await response.json();
  } catch {
    return null;
  }
}

export function parseLoginUser(value: unknown): User | null {
  if (!value || typeof value !== "object") return null;

  const candidate = value as Record<string, unknown>;
  if (
    typeof candidate.id !== "string" ||
    typeof candidate.username !== "string" ||
    typeof candidate.email !== "string" ||
    typeof candidate.tenant !== "string" ||
    !Array.isArray(candidate.roles) ||
    !candidate.roles.every((role) => typeof role === "string")
  ) {
    return null;
  }

  // display_name is a UI affordance, not a security field — fall back to
  // username when the backend omits it (the gateway's /api/v1/auth/login
  // response does not currently include display_name).
  const displayName =
    typeof candidate.display_name === "string" && candidate.display_name
      ? candidate.display_name
      : candidate.username;

  // Accept both camelCase (legacy/synthetic) and snake_case (gateway wire
  // format) for the optional timestamp fields. Same tolerance pattern
  // buildSamlFragmentUser uses to cope with cross-IdP variations.
  const createdAt =
    typeof candidate.createdAt === "string"
      ? candidate.createdAt
      : typeof candidate.created_at === "string"
        ? candidate.created_at
        : undefined;
  const lastLogin =
    typeof candidate.lastLogin === "string"
      ? candidate.lastLogin
      : typeof candidate.last_login_at === "string"
        ? candidate.last_login_at
        : undefined;

  return {
    id: candidate.id,
    username: candidate.username,
    email: candidate.email,
    display_name: displayName,
    roles: candidate.roles,
    tenant: candidate.tenant,
    ...(createdAt !== undefined ? { createdAt } : {}),
    ...(lastLogin !== undefined ? { lastLogin } : {}),
  };
}

function buildSamlFragmentUser(fragment: URLSearchParams): User | null {
  const username =
    fragment.get("username")?.trim() ||
    fragment.get("email")?.trim() ||
    fragment.get("user_id")?.trim() ||
    "";
  if (!username) return null;

  const role = fragment.get("role")?.trim() || "viewer";
  return {
    id: fragment.get("user_id")?.trim() || username,
    username,
    email: fragment.get("email")?.trim() || "",
    display_name: fragment.get("display_name")?.trim() || username,
    tenant: fragment.get("tenant")?.trim() || "default",
    roles: [role],
  };
}

export function parseSamlCallbackHash(hash: string): { token: string; user: User } | null {
  const normalized = hash.startsWith("#") ? hash.slice(1) : hash;
  if (!normalized) return null;

  const fragment = new URLSearchParams(normalized);
  const token = fragment.get("token")?.trim();
  if (!token) return null;

  const user = buildSamlFragmentUser(fragment);
  if (!user) return null;

  return { token, user };
}

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

/** Validate API URL is same-origin or relative path — blocks open redirect in OIDC/SAML flows. */
export function isSafeApiUrl(url: string): string {
  const fallback = "/api/v1";
  const trimmed = url.trim();
  if (!trimmed) return fallback;

  // Relative paths starting with / are safe (block protocol-relative //)
  if (trimmed.startsWith("/") && !trimmed.startsWith("//")) return trimmed;

  // Absolute URLs must be same-origin
  try {
    const parsed = new URL(trimmed);
    if (parsed.origin === window.location.origin) return trimmed;
  } catch {
    // Not a valid absolute URL — reject
  }

  return fallback;
}

export function buildSamlRedirectTarget(returnUrl: string): string {
  const target = new URL("/login", window.location.origin);
  if (returnUrl && returnUrl !== "/") {
    target.searchParams.set("returnUrl", returnUrl);
  }
  return target.toString();
}

export function buildSamlLoginHref(apiUrl: string, returnUrl: string): string {
  const baseUrl = isSafeApiUrl(apiUrl);
  const normalizedBaseUrl = baseUrl.endsWith("/") ? baseUrl.slice(0, -1) : baseUrl;
  const loginUrl = new URL(`${normalizedBaseUrl}/auth/sso/saml/login`, window.location.origin);
  loginUrl.searchParams.set("redirect", buildSamlRedirectTarget(returnUrl));
  return loginUrl.toString();
}

export function buildOidcLoginHref(apiUrl: string, returnUrl: string): string {
  const baseUrl = isSafeApiUrl(apiUrl);
  const normalizedBaseUrl = baseUrl.endsWith("/") ? baseUrl.slice(0, -1) : baseUrl;
  const loginUrl = new URL(`${normalizedBaseUrl}/auth/sso/oidc/login`, window.location.origin);
  loginUrl.searchParams.set("redirect", buildSamlRedirectTarget(returnUrl));
  return loginUrl.toString();
}

function parseSSOErrorHash(hash: string): string | null {
  const normalized = hash.startsWith("#") ? hash.slice(1) : hash;
  if (!normalized) return null;
  const fragment = new URLSearchParams(normalized);
  const error = fragment.get("error")?.trim();
  if (!error) return null;
  const description = fragment.get("error_description")?.trim();
  return description || error;
}

const authModes: {
  id: AuthMode;
  label: string;
  icon: React.ReactNode;
  description: string;
}[] = [
  {
    id: "api_key",
    label: "API Key",
    icon: <KeyRound className="w-4 h-4" />,
    description: "Connect with an API key",
  },
  {
    id: "password",
    label: "Password",
    icon: <Lock className="w-4 h-4" />,
    description: "Username & password login",
  },
  {
    id: "oidc",
    label: "OIDC / SSO",
    icon: <Globe className="w-4 h-4" />,
    description: "OpenID Connect provider",
  },
  {
    id: "saml",
    label: "SAML / Enterprise",
    icon: <Building2 className="w-4 h-4" />,
    description: "Enterprise SAML SSO",
  },
];

export default function LoginPage() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const login = useConfigStore((s) => s.login);
  const configuredApiBase = useConfigStore((s) => s.apiBaseUrl);
  const { data: authConfig } = useAuthConfig();
  const returnUrl = isSafeReturnUrl(searchParams.get("returnUrl"));
  const [authMode, setAuthMode] = useState<AuthMode>("api_key");
  const [showModeSelector, setShowModeSelector] = useState(false);
  // Default-tab resolution runs once when auth-config arrives; tracked so a
  // user's later manual tab choice is never overridden by a refetch.
  const [defaultModeApplied, setDefaultModeApplied] = useState(false);

  // API Key fields. Pre-fill the endpoint with the runtime-configured base
  // (from public/config.json) when present, otherwise the relative `/api/v1`
  // nginx-proxy default — so a fresh install shows a working value instead of
  // a blank box. isSafeApiUrl() still treats empty as the same default.
  const [apiUrl, setApiUrl] = useState(configuredApiBase || "/api/v1");
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

  useEffect(() => {
    const normalizedHash = window.location.hash.startsWith("#")
      ? window.location.hash.slice(1)
      : window.location.hash;
    const token = new URLSearchParams(normalizedHash).get("token")?.trim();
    if (!token) return;

    const payload = parseSamlCallbackHash(window.location.hash);
    if (!payload) {
      showErrorToast("SSO login finished, but the gateway returned incomplete user details");
      window.history.replaceState(null, "", window.location.pathname + window.location.search);
      return;
    }

    // SAML callback: the gateway has set the `cordum_session` httpOnly cookie;
    // payload.token is echoed in the URL fragment for backward-compat with
    // non-browser clients. We deliberately do NOT store the token — cookie
    // auth is the source of truth for browser sessions.
    login({ mode: "session" }, payload.user);
    showSuccessToast("Signed in");
    window.history.replaceState(null, "", window.location.pathname + window.location.search);
    navigate(returnUrl, { replace: true });
  }, [login, navigate, returnUrl]);

  useEffect(() => {
    const message = parseSSOErrorHash(window.location.hash);
    if (!message) return;
    showErrorToast(message);
    window.history.replaceState(null, "", window.location.pathname + window.location.search);
  }, []);

  // Pick the most useful default tab once the gateway's auth-config loads:
  // when password auth is enabled (the common self-hosted setup), open the
  // Password tab with `admin` pre-filled so the documented default login is
  // one field away. SSO-only or API-key-only deployments keep the API Key
  // default. Applied once; manual tab switches afterward are preserved.
  useEffect(() => {
    if (defaultModeApplied || !authConfig) return;
    if (authConfig.password_enabled) {
      setAuthMode("password");
      setUsername((current) => current || "admin");
    }
    setDefaultModeApplied(true);
  }, [authConfig, defaultModeApplied]);

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
      if (!res.ok) {
        const msg =
          res.status === 401 || res.status === 403
            ? "Invalid API key"
            : res.status >= 500
              ? "Server error — try again later"
              : `Connection failed (HTTP ${res.status})`;
        showErrorToast(msg);
        return;
      }
      const rawUser = await readJsonIfOk<Record<string, unknown>>(res);
      const user = parseLoginUser(rawUser);
      if (!user) {
        showErrorToast(
          "Connected, but the server returned an invalid user response",
        );
        return;
      }
      login({ mode: "apikey", key: apiKey.trim() }, user);
      showSuccessToast("Connected to Cordum");
      navigate(returnUrl);
    } catch (err) {
      if (err instanceof DOMException && err.name === "TimeoutError") {
        showErrorToast("Request timed out — check your connection");
      } else {
        showErrorToast("Cannot reach API server — check the endpoint URL");
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
        body: JSON.stringify({
          username: username.trim(),
          password: password.trim(),
        }),
        signal: AbortSignal.timeout(LOGIN_TIMEOUT),
      });
      if (!res.ok) {
        showErrorToast("Invalid credentials");
        return;
      }
      const data = await readJsonIfOk<{
        token?: string;
        user?: Record<string, unknown>;
      }>(res);
      if (!data) {
        showErrorToast(
          "Login succeeded, but the server returned an invalid session payload",
        );
        return;
      }
      // Password login is session-based: the gateway sets the `cordum_session`
      // httpOnly cookie which the browser sends automatically on every
      // subsequent fetch. The token in the body is a backward-compat echo for
      // non-browser clients — we deliberately do NOT store it as `apiKey`
      // (that would cause apiClient to send it as `X-API-Key`, which the
      // gateway rejects → instant logout loop).
      //
      // We still keep the parsed-user-or-fallback resolution so the UI has
      // an identity to render even if the server omits the optional `user`
      // body field; the cookie is the auth artefact, not the user object.
      const parsedUser = parseLoginUser(data.user);
      login(
        { mode: "session" },
        parsedUser || buildPasswordFallbackUser(username),
      );
      showSuccessToast("Logged in");
      navigate(returnUrl);
    } catch (err) {
      if (err instanceof DOMException && err.name === "TimeoutError") {
        showErrorToast("Request timed out — check your connection");
      } else {
        showErrorToast("Cannot reach API server — check the endpoint URL");
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
    window.location.href = buildOidcLoginHref(baseUrl, returnUrl);
  };

  const handleSamlLogin = () => {
    const raw = apiUrl.trim();
    const baseUrl = isSafeApiUrl(raw);
    if (raw && baseUrl !== raw) {
      toast.warning("Unsafe API URL blocked — using default endpoint");
    }
    toast.info("Redirecting to SAML IdP...");
    window.location.href = buildSamlLoginHref(baseUrl, returnUrl);
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
            </motion.div>
          )}

          {/* Password mode */}
          {authMode === "password" && (
            <motion.div
              key="password"
              initial={{ opacity: 0, height: 0 }}
              animate={{ opacity: 1, height: "auto" }}
              exit={{ opacity: 0, height: 0 }}
              className="space-y-4"
            >
              <div className="space-y-2">
                <label className="text-xs font-mono font-semibold text-muted-foreground uppercase tracking-[0.08em]">
                  Username
                </label>
                <input
                  type="text"
                  placeholder="admin"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  className="h-9 w-full rounded-2xl border border-border bg-card/80 px-3 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/30 font-mono"
                />
              </div>
              <div className="space-y-2">
                <label className="text-xs font-mono font-semibold text-muted-foreground uppercase tracking-[0.08em]">
                  Password
                </label>
                <div className="relative">
                  <Lock className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
                  <input
                    type="password"
                    placeholder="Enter password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    onKeyDown={(e) => e.key === "Enter" && handleSubmit()}
                    className="h-9 w-full rounded-2xl border border-border bg-card/80 pl-9 pr-3 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/30 font-mono"
                  />
                </div>
              </div>
            </motion.div>
          )}

          {/* OIDC mode */}
          {authMode === "oidc" && (
            <motion.div
              key="oidc"
              initial={{ opacity: 0, height: 0 }}
              animate={{ opacity: 1, height: "auto" }}
              exit={{ opacity: 0, height: 0 }}
              className="text-center py-2"
            >
              <Globe className="w-8 h-8 text-cordum mx-auto mb-2" />
              <p className="text-xs text-muted-foreground">
                You will be redirected to your OIDC provider to authenticate.
              </p>
            </motion.div>
          )}

          {/* SAML mode */}
          {authMode === "saml" && (
            <motion.div
              key="saml"
              initial={{ opacity: 0, height: 0 }}
              animate={{ opacity: 1, height: "auto" }}
              exit={{ opacity: 0, height: 0 }}
              className="text-center py-2"
            >
              <Building2 className="w-8 h-8 text-cordum mx-auto mb-2" />
              <p className="text-xs text-muted-foreground">
                You will be redirected to your enterprise identity provider.
              </p>
            </motion.div>
          )}

          <Button
            variant="primary"
            className="w-full rounded-full bg-primary text-primary-foreground shadow-glow hover:bg-primary/90"
            loading={loading}
            onClick={handleSubmit}
          >
            {authMode === "api_key" && "Connect"}
            {authMode === "password" && "Sign In"}
            {authMode === "oidc" && "Continue with OIDC"}
            {authMode === "saml" && "Continue with SAML"}
            <ArrowRight className="w-3.5 h-3.5 ml-1" />
          </Button>
        </div>

        <p className="text-center text-xs text-muted-foreground">
          Need help? Check the{" "}
          <a
            href="https://cordum.io/docs"
            className="text-cordum hover:text-cordum-bright transition-colors"
          >
            documentation
          </a>
        </p>
      </motion.div>
    </div>
  );
}
