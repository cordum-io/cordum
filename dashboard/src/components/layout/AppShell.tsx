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
];

const settingsItem = { path: "/settings", label: "Settings", icon: Settings };

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
        <aside className="hidden w-64 flex-col gap-6 border-r border-border bg-[color:var(--surface-glass)] px-6 py-8 backdrop-blur-xl lg:flex">
          <div className="space-y-2">
            <div className="flex items-center gap-3">
              <img src="/assets/cordum-logo.png" alt="Cordum logo" className="h-9 w-auto object-contain dark:brightness-0 dark:invert" />
              <div>
                <div className="text-xs uppercase tracking-[0.25em] text-muted">Cordum</div>
                <h1 className="font-display text-2xl font-semibold text-ink">Control Plane</h1>
              </div>
            </div>
            <p className="text-xs text-muted">AI orchestration, safety, and runtime clarity.</p>
          </div>
          <nav className="mt-6 flex flex-1 flex-col gap-1">
            <div className="flex flex-1 flex-col gap-1 overflow-y-auto">
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
                        "flex items-center gap-3 rounded-2xl px-4 py-3 text-sm font-semibold transition",
                        isActive
                          ? "bg-[color:rgba(15,127,122,0.16)] text-accent"
                          : "text-ink hover:bg-[color:rgba(15,127,122,0.08)]"
                      )
                    }
                  >
                    <Icon className="h-4 w-4" />
                    {item.label}
                    {badgeText ? (
                      <span
                        className={cn(
                          "ml-auto rounded-full px-2 py-0.5 text-[10px] font-semibold",
                          badge.variant === "danger"
                            ? "bg-[color:rgba(184,58,58,0.14)] text-danger"
                            : "bg-[color:rgba(197,138,28,0.18)] text-warning"
                        )}
                      >
                        {badgeText}
                      </span>
                    ) : null}
                  </NavLink>
                );
              })}
            </div>
            <div className="mt-auto border-t border-border pt-3">
              <NavLink
                to={settingsItem.path}
                className={({ isActive }) =>
                  cn(
                    "flex items-center gap-3 rounded-2xl px-4 py-3 text-sm font-semibold transition",
                    isActive
                      ? "bg-[color:rgba(15,127,122,0.16)] text-accent"
                      : "text-ink hover:bg-[color:rgba(15,127,122,0.08)]"
                  )
                }
              >
                <Settings className="h-4 w-4" />
                {settingsItem.label}
              </NavLink>
            </div>
          </nav>
          <div className="rounded-2xl border border-border bg-white/70 p-4 text-xs text-muted">
            <div className="mb-2 flex items-center justify-between">
              <span className="font-semibold text-ink">Bus stream</span>
              <ConnectionIndicator />
            </div>
            <div className="flex items-center gap-2 text-[11px]">
              <Network className="h-3 w-3" />
              <span className="truncate">{apiBaseUrl || "same origin"}</span>
            </div>
          </div>
        </aside>
        <div className="flex flex-1 flex-col">
          <EnvironmentBorder />
          <header className="sticky top-0 z-10 border-b border-border bg-[color:var(--surface-glass)] px-4 py-4 backdrop-blur-xl lg:px-10">
            <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
              <div>
                <Breadcrumbs />
                <div className="flex items-center gap-2">
                  <img src="/assets/cordum-logo.png" alt="Cordum logo" className="h-6 w-auto object-contain dark:brightness-0 dark:invert" />
                  <h2 className="font-display text-xl font-semibold text-ink">Cordum Console</h2>
                  <EnvironmentBadge />
                </div>
              </div>
              <div className="flex flex-1 flex-col gap-3 lg:flex-row lg:items-center lg:justify-end">
                <div className="relative flex-1 lg:max-w-md">
                  <Input
                    value={globalSearch}
                    onChange={(event) => setGlobalSearch(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") {
                        const next = event.currentTarget.value.trim();
                        navigate(next ? `/search?q=${encodeURIComponent(next)}` : "/search");
                      }
                    }}
                    placeholder="Search runs, workflows, packs, jobs..."
                  />
                </div>
                <Button variant="outline" size="sm" type="button" onClick={toggleTheme}>
                  {theme === "light" && <Sun className="h-4 w-4" />}
                  {theme === "dark" && <Moon className="h-4 w-4" />}
                  {theme === "system" && <Monitor className="h-4 w-4" />}
                  {theme === "light" ? "Light" : theme === "dark" ? "Dark" : "System"}
                </Button>
                <button
                  onClick={() => setCommandOpen(true)}
                  className="rounded-full border border-border bg-white/70 px-4 py-2 text-xs font-semibold uppercase tracking-[0.2em] text-ink transition hover:border-accent"
                  type="button"
                >
                  Command
                </button>
                {requiresAuth && apiKey ? (
                  <div className="flex items-center gap-2">
                    <div className="flex items-center gap-2 rounded-full border border-border bg-white/70 px-3 py-2 text-xs text-ink">
                      <UserCircle className="h-4 w-4" />
                      <div className="leading-tight">
                        <div className="text-xs font-semibold">{displayName}</div>
                        <div className="text-[10px] text-muted">
                          {tenantLabel}
                          {roleLabel ? ` · ${roleLabel}` : ""}
                        </div>
                      </div>
                    </div>
                    <Button variant="outline" size="sm" type="button" onClick={onLogout} disabled={loggingOut}>
                      <LogOut className="h-4 w-4" />
                      {loggingOut ? "Logging out" : "Logout"}
                    </Button>
                  </div>
                ) : null}
              </div>
            </div>
            <nav className="mt-4 flex gap-2 overflow-x-auto pb-2 lg:hidden">
              {[...navItems, settingsItem].map((item) => {
                const Icon = item.icon;
                const badge = navBadges[item.path];
                const badgeText = badge && badge.count > 0 ? formatCount(badge.count) : "";
                return (
                  <NavLink
                    key={item.path}
                    to={item.path}
                    className={({ isActive }) =>
                      cn(
                        "flex items-center gap-2 rounded-full px-4 py-2 text-xs font-semibold uppercase tracking-[0.2em]",
                        isActive ? "bg-[color:rgba(15,127,122,0.16)] text-accent" : "border border-border text-ink"
                      )
                    }
                  >
                    <Icon className="h-3 w-3" />
                    {item.label}
                    {badgeText ? (
                      <span
                        className={cn(
                          "rounded-full px-2 py-0.5 text-[10px] font-semibold",
                          badge.variant === "danger"
                            ? "bg-[color:rgba(184,58,58,0.14)] text-danger"
                            : "bg-[color:rgba(197,138,28,0.18)] text-warning"
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
          <main className="flex-1 px-4 py-8 lg:px-10">{children}</main>
        </div>
      </div>
    </div>
  );
}
