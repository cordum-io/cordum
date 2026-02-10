import { useEffect, useState, useCallback } from "react";
import { useNavigate, useLocation } from "react-router-dom";
import { Clock } from "lucide-react";
import { useConfigStore } from "../state/config";
import { useAuthConfig } from "../hooks/useAuthConfig";
import { get } from "../api/client";
import { Button } from "./ui/Button";

// ---------------------------------------------------------------------------
// Parse session_ttl string (e.g. "1h", "30m", "3600s") to milliseconds
// ---------------------------------------------------------------------------

function parseTtlMs(ttl: string | undefined): number {
  if (!ttl) return 3_600_000; // default 1 hour
  const match = ttl.match(/^(\d+)\s*(h|m|s)?$/i);
  if (!match) return 3_600_000;
  const value = Number(match[1]);
  const unit = (match[2] ?? "s").toLowerCase();
  if (unit === "h") return value * 3_600_000;
  if (unit === "m") return value * 60_000;
  return value * 1_000;
}

// ---------------------------------------------------------------------------
// Format remaining milliseconds as "Xm Ys"
// ---------------------------------------------------------------------------

function formatRemaining(ms: number): string {
  const totalSeconds = Math.max(0, Math.ceil(ms / 1_000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes > 0) return `${minutes}m ${seconds}s`;
  return `${seconds}s`;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

const WARNING_THRESHOLD_MS = 5 * 60_000; // 5 minutes
const CHECK_INTERVAL_MS = 30_000; // 30 seconds

export function SessionTimeoutWarning() {
  const navigate = useNavigate();
  const location = useLocation();
  const { data: authConfig } = useAuthConfig();
  const loginTimestamp = useConfigStore((s) => s.loginTimestamp);
  const logout = useConfigStore((s) => s.logout);
  const refreshLoginTimestamp = useConfigStore((s) => s.refreshLoginTimestamp);

  const requiresAuth =
    !!authConfig &&
    (authConfig.password_enabled ||
      !!authConfig.user_auth_enabled ||
      authConfig.saml_enabled);

  const ttlMs = parseTtlMs(authConfig?.session_ttl);

  const [remainingMs, setRemainingMs] = useState<number | null>(null);

  const computeRemaining = useCallback(() => {
    if (!loginTimestamp) return null;
    return ttlMs - (Date.now() - loginTimestamp);
  }, [loginTimestamp, ttlMs]);

  // Check remaining time on interval
  useEffect(() => {
    if (!requiresAuth || !loginTimestamp) {
      setRemainingMs(null);
      return;
    }

    setRemainingMs(computeRemaining());
    const id = setInterval(() => setRemainingMs(computeRemaining()), CHECK_INTERVAL_MS);
    return () => clearInterval(id);
  }, [requiresAuth, loginTimestamp, computeRemaining]);

  // Auto-redirect when expired
  useEffect(() => {
    if (remainingMs !== null && remainingMs <= 0) {
      const returnUrl = location.pathname + location.search;
      logout();
      navigate(`/login?returnUrl=${encodeURIComponent(returnUrl)}`, { replace: true });
    }
  }, [remainingMs, logout, navigate, location.pathname, location.search]);

  const handleExtend = useCallback(async () => {
    try {
      await get("/auth/session");
      refreshLoginTimestamp();
    } catch {
      // If refresh fails, user will be redirected when session expires
    }
  }, [refreshLoginTimestamp]);

  const handleLogout = useCallback(() => {
    logout();
    navigate("/login", { replace: true });
  }, [logout, navigate]);

  // Don't render if auth not configured or no timestamp or not in warning zone
  if (!requiresAuth || !loginTimestamp) return null;
  if (remainingMs === null || remainingMs > WARNING_THRESHOLD_MS) return null;
  if (remainingMs <= 0) return null;

  return (
    <div className="fixed inset-x-0 top-0 z-50 border-b border-warning/30 bg-warning/10 px-4 py-2">
      <div className="mx-auto flex max-w-6xl items-center justify-between gap-4">
        <div className="flex items-center gap-2 text-sm text-warning">
          <Clock className="h-4 w-4 shrink-0" />
          <span>
            Your session expires in{" "}
            <span className="font-semibold">{formatRemaining(remainingMs)}</span>
          </span>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={handleExtend}>
            Extend
          </Button>
          <Button variant="ghost" size="sm" onClick={handleLogout}>
            Log out
          </Button>
        </div>
      </div>
    </div>
  );
}
