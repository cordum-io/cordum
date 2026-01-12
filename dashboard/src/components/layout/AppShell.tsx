import { NavLink, useLocation, useNavigate } from "react-router-dom";
import {
  Activity,
  Boxes,
  Gauge,
  GitGraph,
  LayoutGrid,
  ListChecks,
  Moon,
  Network,
  Shield,
  Sun,
  Workflow,
  Wrench,
} from "lucide-react";
import { useEffect, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Input } from "../ui/Input";
import { Button } from "../ui/Button";
import { api } from "../../lib/api";
import { formatCount } from "../../lib/format";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../state/ui";
import { useEventStore } from "../../state/events";
import { useConfigStore } from "../../state/config";

const navItems = [
  { path: "/", label: "Home", icon: LayoutGrid },
  { path: "/runs", label: "Runs", icon: Activity },
  { path: "/jobs", label: "Jobs", icon: ListChecks },
  { path: "/workflows", label: "Workflows", icon: Workflow },
  { path: "/packs", label: "Packs", icon: Boxes },
  { path: "/policy", label: "Policy", icon: Shield },
  { path: "/system", label: "System", icon: Gauge },
  { path: "/tools", label: "Tools", icon: Wrench },
  { path: "/trace", label: "Trace", icon: GitGraph },
];

export function AppShell({ children }: { children: ReactNode }) {
  const location = useLocation();
  const navigate = useNavigate();
  const globalSearch = useUiStore((state) => state.globalSearch);
  const setGlobalSearch = useUiStore((state) => state.setGlobalSearch);
  const setCommandOpen = useUiStore((state) => state.setCommandOpen);
  const theme = useUiStore((state) => state.theme);
  const toggleTheme = useUiStore((state) => state.toggleTheme);
  const wsStatus = useEventStore((state) => state.status);
  const apiBaseUrl = useConfigStore((state) => state.apiBaseUrl);
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

  const approvalsCount = approvalsQuery.data?.items.length ?? 0;
  const dlqCount = dlqQuery.data?.items.length ?? 0;
  const navBadges: Record<string, { count: number; variant: "warning" | "danger" }> = {
    "/policy": { count: approvalsCount, variant: "warning" },
    "/system": { count: dlqCount, variant: "danger" },
  };

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
    if (typeof window !== "undefined") {
      window.localStorage.setItem("cordum-theme", theme);
    }
  }, [theme]);

  return (
    <div className="min-h-screen">
      <div className="flex min-h-screen">
        <aside className="hidden w-64 flex-col gap-6 border-r border-border bg-[color:var(--surface-glass)] px-6 py-8 backdrop-blur-xl lg:flex">
          <div className="space-y-2">
            <div className="text-xs uppercase tracking-[0.25em] text-muted">Cordum</div>
            <h1 className="font-display text-2xl font-semibold text-ink">Control Plane</h1>
            <p className="text-xs text-muted">AI orchestration, safety, and runtime clarity.</p>
          </div>
          <nav className="mt-6 flex flex-1 flex-col gap-3">
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
          </nav>
          <div className="rounded-2xl border border-border bg-white/70 p-4 text-xs text-muted">
            <div className="mb-2 flex items-center justify-between">
              <span className="font-semibold text-ink">Bus stream</span>
              <span
                className={cn(
                  "rounded-full px-2 py-1 text-[10px] font-semibold uppercase",
                  wsStatus === "connected"
                    ? "bg-[color:rgba(31,122,87,0.15)] text-success"
                    : wsStatus === "connecting"
                    ? "bg-[color:rgba(197,138,28,0.15)] text-warning"
                    : "bg-[color:rgba(184,58,58,0.14)] text-danger"
                )}
              >
                {wsStatus}
              </span>
            </div>
            <div className="flex items-center gap-2 text-[11px]">
              <Network className="h-3 w-3" />
              <span className="truncate">{apiBaseUrl || "same origin"}</span>
            </div>
          </div>
        </aside>
        <div className="flex flex-1 flex-col">
          <header className="sticky top-0 z-10 border-b border-border bg-[color:var(--surface-glass)] px-4 py-4 backdrop-blur-xl lg:px-10">
            <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
              <div>
                <p className="text-xs uppercase tracking-[0.3em] text-muted">{location.pathname}</p>
                <h2 className="font-display text-xl font-semibold text-ink">Cordum Console</h2>
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
                  {theme === "dark" ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
                  {theme === "dark" ? "Light" : "Dark"}
                </Button>
                <button
                  onClick={() => setCommandOpen(true)}
                  className="rounded-full border border-border bg-white/70 px-4 py-2 text-xs font-semibold uppercase tracking-[0.2em] text-ink transition hover:border-accent"
                  type="button"
                >
                  Command
                </button>
              </div>
            </div>
            <nav className="mt-4 flex gap-2 overflow-x-auto pb-2 lg:hidden">
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
          <main className="flex-1 px-4 py-8 lg:px-10">{children}</main>
        </div>
      </div>
    </div>
  );
}
