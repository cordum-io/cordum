import { NavLink, useLocation, useNavigate } from "react-router-dom";
import { Breadcrumbs } from "./Breadcrumbs";
import {
  AlertTriangle,
  Boxes,
  Cpu,
  FileText,
  LayoutGrid,
  ListChecks,
  LogOut,
  Monitor,
  Moon,
  Network,
  Settings,
  Shield,
  Sun,
  UserCheck,
  UserCircle,
  Workflow,
} from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Input } from "../ui/Input";
import { Button } from "../ui/Button";
import { api } from "../../lib/api";
import { formatCount } from "../../lib/format";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../state/ui";
import { usePresenceCleanup } from "../../state/events";
import { ConnectionIndicator } from "../ConnectionIndicator";
import { useConfigStore } from "../../state/config";
import { useAuthConfig } from "../../hooks/useAuthConfig";
import { MaintenanceBanner } from "./MaintenanceBanner";
import { EnvironmentBorder, EnvironmentBadge } from "./EnvironmentBanner";
import { logger } from "../../lib/logger";

const navItems = [
  { path: "/", label: "Overview", icon: LayoutGrid },
  { path: "/jobs", label: "Jobs", icon: ListChecks },
  { path: "/workflows", label: "Workflows", icon: Workflow },
  { path: "/agents", label: "Agent Fleet", icon: Cpu },
  { path: "/approvals", label: "Approvals", icon: UserCheck },
  { path: "/policies", label: "Policy Studio", icon: Shield },
  { path: "/packs", label: "Packs", icon: Boxes },
  { path: "/dlq", label: "Dead Letters", icon: AlertTriangle },
  { path: "/audit", label: "Audit Log", icon: FileText },
  { path: "/settings", label: "Settings", icon: Settings },
];

export function AppShell({ children }: { children: ReactNode }) {
  const location = useLocation();
  const navigate = useNavigate();
  const globalSearch = useUiStore((state) => state.globalSearch);
  const setGlobalSearch = useUiStore((state) => state.setGlobalSearch);
  const setCommandOpen = useUiStore((state) => state.setCommandOpen);
  const theme = useUiStore((state) => state.theme);
  const resolvedTheme = useUiStore((state) => state.resolvedTheme);
  const toggleTheme = useUiStore((state) => state.toggleTheme);
  const syncSystemTheme = useUiStore((state) => state.syncSystemTheme);
  const apiBaseUrl = useConfigStore((state) => state.apiBaseUrl);
  const apiKey = useConfigStore((state) => state.apiKey);
  const logout = useConfigStore((state) => state.logout);
  const { data: authConfig } = useAuthConfig();
  const [loggingOut, setLoggingOut] = useState(false);

  usePresenceCleanup();
  const requiresAuth = !!authConfig && (
    authConfig.password_enabled ||
    authConfig.user_auth_enabled ||
    authConfig.saml_enabled
  );
  const sessionQuery = useQuery({
    queryKey: ["auth-session"],
    queryFn: () => api.getSession(),
    enabled: requiresAuth && !!apiKey,
    staleTime: 60_000,
    retry: false,
  });
  const user = sessionQuery.data?.user;
  const approvalsQuery = useQuery({
    queryKey: ["approvals", "nav"],
    queryFn: () => api.listApprovals(200),
    staleTime: 30_000,
  });
  const dlqQuery = useQuery({
    queryKey: ["dlq", "nav"],
    queryFn: () => api.listDLQPage(200),
    staleTime: 30_000,
  });

  const approvalsCount = approvalsQuery.data?.items?.length ?? 0;
  const dlqCount = dlqQuery.data?.items?.length ?? 0;
  const navBadges: Record<string, { count: number; variant: "warning" | "danger" }> = {
    "/approvals": { count: approvalsCount, variant: "warning" },
    "/dlq": { count: dlqCount, variant: "danger" },
  };

  // Apply resolved theme (always 'light' or 'dark') to document
  useEffect(() => {
    document.documentElement.dataset.theme = resolvedTheme;
    document.documentElement.style.colorScheme = resolvedTheme;
  }, [resolvedTheme]);

  // Persist theme preference (may be 'system')
  useEffect(() => {
    window.localStorage.setItem("cordum-theme", theme);
  }, [theme]);

  // Listen for OS color scheme changes when theme is 'system'
  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = () => syncSystemTheme();
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, [syncSystemTheme]);

  const displayName = user?.display_name || user?.email || user?.username || "Signed in";
  const roleLabel = user?.roles?.length ? user.roles.join(", ") : "";
  const tenantLabel = user?.tenant || authConfig?.default_tenant || "default";

  const onLogout = async () => {
    if (loggingOut) {
      return;
    }
    logger.info("app-shell", "Logging out");
    setLoggingOut(true);
    try {
      await api.logout();
    } catch {
      logger.warn("app-shell", "Logout API call failed, clearing local session");
    }
    logout();
    setLoggingOut(false);
    navigate("/login");
  };

  return (
    <div className="min-h-screen">
      <div className="flex min-h-screen">
<aside className="hidden h-screen w-60 shrink-0 flex-col gap-4 border-r border-border bg-surface px-5 py-6 lg:sticky lg:top-0 lg:flex">
          <div className="flex items-center gap-3">
            <img src="/assets/cordum-logo.png" alt="Cordum logo" className="h-8 w-auto object-contain dark:brightness-0 dark:invert" />
            <div>
              <h1 className="font-display text-lg font-semibold text-ink">Cordum</h1>
              <p className="text-[11px] text-muted">Control Plane</p>
            </div>
          </div>
          <div className="rounded-lg border border-border bg-surface2 p-3 text-xs text-muted">
            <div className="mb-1.5 flex items-center justify-between">
              <span className="font-medium text-ink">Connection</span>
              <ConnectionIndicator />
            </div>
            <div className="flex items-center gap-2 text-[10px]">
              <Network className="h-3 w-3" />
              <span className="truncate">{apiBaseUrl || "same origin"}</span>
            </div>
          </div>
          <nav className="mt-2 flex min-h-0 flex-1 flex-col gap-0.5 overflow-hidden">
            <div className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto">
              {navItems.map((item) => {
                const Icon = item.icon;
                const badge = navBadges[item.path];
                const badgeText = badge && badge.count > 0 ? formatCount(badge.count) : "";
                return (
                  <NavLink
                    key={item.path}
                    to={item.path}
                    className={({ isActive }) =>
                      cn(
                        "flex items-center gap-3 rounded-lg px-3 py-2 text-[13px] font-medium transition-colors",
                        isActive
                          ? "bg-accent/10 text-accent"
                          : "text-ink hover:bg-surface2"
                      )
                    }
                  >
                    <Icon className="h-4 w-4" />
                    {item.label}
                    {badgeText ? (
                      <span
                        className={cn(
                          "ml-auto rounded px-1.5 py-0.5 text-[10px] font-medium",
                          badge.variant === "danger"
                            ? "bg-danger/10 text-danger"
                            : "bg-warning/10 text-warning"
                        )}
                      >
                        {badgeText}
                      </span>
                    ) : null}
                  </NavLink>
                );
              })}
            </div>
          </nav>
        </aside>
        <div className="flex flex-1 flex-col">
          <EnvironmentBorder />
<header className="sticky top-0 z-10 border-b border-border bg-surface px-4 py-3 lg:px-8">
            <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
              <div>
                <Breadcrumbs />
                <div className="flex items-center gap-2">
                  <EnvironmentBadge />
                </div>
              </div>
              <div className="flex flex-1 flex-col gap-2 lg:flex-row lg:items-center lg:justify-end">
                <div className="relative flex-1 lg:max-w-sm">
                  <Input
                    value={globalSearch}
                    onChange={(event) => setGlobalSearch(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") {
                        const next = event.currentTarget.value.trim();
                        navigate(next ? `/search?q=${encodeURIComponent(next)}` : "/search");
                      }
                    }}
                    placeholder="Search..."
                  />
                </div>
                <Button variant="outline" size="sm" type="button" onClick={toggleTheme}>
                  {theme === "light" && <Sun className="h-4 w-4" />}
                  {theme === "dark" && <Moon className="h-4 w-4" />}
                  {theme === "system" && <Monitor className="h-4 w-4" />}
                </Button>
                <button
                  onClick={() => setCommandOpen(true)}
                  className="hidden items-center gap-2 rounded-lg border border-border bg-surface px-3 py-1.5 text-xs text-muted transition hover:border-accent hover:text-ink lg:flex"
                  type="button"
                >
                  <span>Search</span>
                  <kbd className="rounded bg-surface2 px-1.5 py-0.5 text-[10px] font-medium">Cmd+K</kbd>
                </button>
{requiresAuth && apiKey ? (
                  <div className="flex items-center gap-2">
                    <div className="flex items-center gap-2 rounded-lg border border-border bg-surface px-3 py-1.5 text-xs text-ink">
                      <UserCircle className="h-4 w-4 text-muted" />
                      <div className="leading-tight">
                        <div className="text-xs font-medium">{displayName}</div>
                        <div className="text-[10px] text-muted">
                          {tenantLabel}
                          {roleLabel ? ` · ${roleLabel}` : ""}
                        </div>
                      </div>
                    </div>
                    <Button variant="outline" size="sm" type="button" onClick={onLogout} disabled={loggingOut}>
                      <LogOut className="h-4 w-4" />
                    </Button>
                  </div>
                ) : null}
              </div>
            </div>
<nav className="mt-3 flex gap-1.5 overflow-x-auto pb-2 lg:hidden">
              {navItems.map((item) => {
                const Icon = item.icon;
                const badge = navBadges[item.path];
                const badgeText = badge && badge.count > 0 ? formatCount(badge.count) : "";
                return (
                  <NavLink
                    key={item.path}
                    to={item.path}
                    className={({ isActive }) =>
                      cn(
                        "flex shrink-0 items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium",
                        isActive ? "bg-accent/10 text-accent" : "border border-border text-ink"
                      )
                    }
                  >
                    <Icon className="h-3.5 w-3.5" />
                    {item.label}
                    {badgeText ? (
                      <span
                        className={cn(
                          "rounded px-1.5 py-0.5 text-[10px] font-medium",
                          badge.variant === "danger"
                            ? "bg-danger/10 text-danger"
                            : "bg-warning/10 text-warning"
                        )}
                      >
                        {badgeText}
                      </span>
                    ) : null}
                  </NavLink>
                );
              })}
            </nav>
          </header>
          <MaintenanceBanner />
          <main className="flex-1 px-4 py-6 lg:px-8">{children}</main>
        </div>
      </div>
    </div>
  );
}
