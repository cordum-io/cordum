import { useState, useEffect } from "react";
import { NavLink, Outlet } from "react-router-dom";
import { Activity, Key, Users, Bell, Layers, Settings, Rocket, Shield, ShieldAlert, Plug2 } from "lucide-react";
import { cn } from "../../lib/utils";
import { useSetupStatus } from "../../hooks/useSetupStatus";
import { SetupChecklist } from "./SetupChecklist";

const NAV_ITEMS = [
  { path: "health", label: "System Health", icon: Activity },
  { path: "keys", label: "API Keys", icon: Key },
  { path: "users", label: "Users & Access", icon: Users },
  { path: "notifications", label: "Notifications", icon: Bell },
  { path: "environments", label: "Environments", icon: Layers },
  { path: "mcp", label: "MCP Server", icon: Plug2 },
  { path: "config", label: "Configuration", icon: Settings },
  { path: "output-safety", label: "Output Safety", icon: ShieldAlert },
  { path: "input-safety", label: "Input Safety", icon: Shield },
] as const;

const SS_AUTO_SHOWN_KEY = "cordum-setup-auto-shown";

export default function SettingsLayout() {
  const setup = useSetupStatus();
  const [drawerOpen, setDrawerOpen] = useState(false);

  const showSetupLink = !setup.dismissed && (setup.isNewInstall || setup.completedCount < setup.totalRequired);

  // Auto-open on first visit to /settings if new install and not dismissed
  useEffect(() => {
    if (setup.isLoading || setup.dismissed || !setup.isNewInstall) return;
    const alreadyShown = sessionStorage.getItem(SS_AUTO_SHOWN_KEY);
    if (!alreadyShown) {
      sessionStorage.setItem(SS_AUTO_SHOWN_KEY, "true");
      setDrawerOpen(true);
    }
  }, [setup.isLoading, setup.dismissed, setup.isNewInstall]);

  return (
    <div className="flex min-h-0 gap-0">
{/* Sidebar — desktop */}
      <nav className="hidden md:flex w-52 shrink-0 flex-col border-r border-border pr-4 sticky top-0 self-start">
        <h1 className="mb-3 font-display text-lg font-semibold text-ink">Settings</h1>
        <ul className="space-y-0.5">
          {NAV_ITEMS.map((item) => {
            const Icon = item.icon;
            return (
              <li key={item.path}>
                <NavLink
                  to={item.path}
                  className={({ isActive }) =>
                    cn(
                      "flex items-center gap-2.5 rounded-lg px-3 py-2 text-[13px] font-medium transition-colors",
                      isActive
                        ? "bg-accent/10 text-accent"
                        : "text-muted hover:bg-surface2 hover:text-ink",
                    )
                  }
                >
                  <Icon className="h-4 w-4 shrink-0" />
                  {item.label}
                </NavLink>
              </li>
            );
          })}
        </ul>

        {/* Setup Guide link */}
        {showSetupLink && (
          <button
            type="button"
            className="mt-auto flex items-center gap-2.5 rounded-lg px-3 py-2 text-[13px] font-medium text-accent transition-colors hover:bg-accent/10"
            onClick={() => setDrawerOpen(true)}
          >
            <span className="relative">
              <Rocket className="h-4 w-4 shrink-0" />
              <span className="absolute -right-0.5 -top-0.5 h-1.5 w-1.5 rounded-full bg-accent animate-pulse" />
            </span>
            Setup Guide
            <span className="ml-auto rounded bg-accent/10 px-1.5 py-0.5 text-[10px] font-medium">
              {setup.completedCount}/{setup.totalRequired}
            </span>
          </button>
        )}
      </nav>

{/* Sidebar — mobile (horizontal scroll) */}
      <nav className="md:hidden -mx-4 mb-4 overflow-x-auto border-b border-border px-4 pb-2">
        <div className="flex items-center gap-1">
          {NAV_ITEMS.map((item) => {
            const Icon = item.icon;
            return (
              <NavLink
                key={item.path}
                to={item.path}
                className={({ isActive }) =>
                  cn(
                    "flex shrink-0 items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors",
                    isActive
                      ? "bg-accent/10 text-accent"
                      : "text-muted hover:text-ink",
                  )
                }
              >
                <Icon className="h-3.5 w-3.5" />
                {item.label}
              </NavLink>
            );
          })}
          {showSetupLink && (
            <button
              type="button"
              className="flex shrink-0 items-center gap-1.5 rounded-lg bg-accent/10 px-3 py-1.5 text-xs font-medium text-accent"
              onClick={() => setDrawerOpen(true)}
            >
              <Rocket className="h-3.5 w-3.5" />
              Setup
            </button>
          )}
        </div>
      </nav>

      {/* Main content */}
      <div className="min-w-0 flex-1 md:pl-6">
        <Outlet />
      </div>

      {/* Setup Checklist Drawer */}
      <SetupChecklist
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        items={setup.items}
        completedCount={setup.completedCount}
        totalRequired={setup.totalRequired}
        onDismissForever={setup.dismiss}
      />
    </div>
  );
}
