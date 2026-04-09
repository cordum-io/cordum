import { NavLink, useLocation, useNavigate } from "react-router-dom";
import { type ReactNode, useState, useEffect, useRef, useCallback } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { cn } from "@/lib/utils";
import { useConfigStore } from "@/state/config";
import { useUiStore } from "@/state/ui";
import { useApprovals } from "@/hooks/useApprovals";
import { useDLQ } from "@/hooks/useDLQ";
import { useLicense } from "@/hooks/useLicense";
import { useQuarantinedJobs } from "@/hooks/useOutputPolicy";
import { useStatus } from "@/hooks/useStatus";
import { useWorkerEvents } from "@/hooks/useWorkers";
import { useKeyboardShortcuts, G_KEY_MAP } from "@/hooks/useKeyboardShortcuts";
import { useDialogA11y } from "@/hooks/useDialogA11y";
import { useReducedMotion } from "framer-motion";
import { CommandPalette } from "@/components/CommandPalette";
import { NotificationPopover } from "@/components/NotificationPopover";
import { ConnectionIndicator } from "@/components/ConnectionIndicator";
import { KeyboardShortcutsDialog } from "@/components/KeyboardShortcuts";
import { TierBadge } from "@/components/TierBadge";
import { TelemetryConsentBanner } from "@/components/TelemetryConsentBanner";
import {
  LayoutGrid,
  ListChecks,
  Workflow,
  Cpu,
  UserCheck,
  AlertTriangle,
  FileText,
  Settings,
  ChevronLeft,
  ChevronRight,
  Moon,
  Sun,
  LogOut,
  Search,
  Command,
  ExternalLink,
  Shield,
  ShieldCheck,
  ShieldAlert,
  GitBranch,
  Package,
  Database,
  Hash,
  Layers,
  Zap,
  Menu,
  X,
  Eye,
} from "lucide-react";

/*
 * Navigation Structure — Revision v2
 * OPERATE → ORCHESTRATE → GOVERN → EXTEND → OBSERVE
 *
 * CTO reads top-down and sees their platform.
 * CISO clicks into GOVERN and finds depth.
 * Approvals is in ORCHESTRATE (it's an operational action, not policy authoring).
 */
interface NavItem {
  path: string;
  label: string;
  icon: typeof LayoutGrid;
  badge?: "approvals" | "dlq" | "quarantine";
  end?: boolean;
}

interface NavSection {
  label: string;
  items: NavItem[];
}

export const APP_SHELL_NAV_SECTIONS: NavSection[] = [
  {
    label: "Operate",
    items: [
      { path: "/", label: "Dashboard", icon: LayoutGrid, end: true },
      { path: "/agents", label: "Agents", icon: Cpu },
      { path: "/jobs", label: "Jobs", icon: ListChecks },
    ],
  },
  {
    label: "Orchestrate",
    items: [
      { path: "/workflows", label: "Workflows", icon: Workflow },
      { path: "/approvals", label: "Approvals", icon: UserCheck, badge: "approvals" },
    ],
  },
  {
    label: "Govern",
    items: [
      { path: "/govern/overview", label: "Policy Studio", icon: Shield },
      { path: "/govern/tenants", label: "Tenants", icon: Layers },
      { path: "/govern/quarantine", label: "Quarantine", icon: ShieldAlert, badge: "quarantine" },
    ],
  },
  {
    label: "Extend",
    items: [
      { path: "/packs", label: "Packs", icon: Package },
      { path: "/topics", label: "Topics", icon: Hash },
      { path: "/schemas", label: "Schemas", icon: Database },
    ],
  },
  {
    label: "Observe",
    items: [
      { path: "/audit", label: "Audit Log", icon: FileText },
      { path: "/dlq", label: "Dead Letters", icon: AlertTriangle },
    ],
  },
];

// g+key navigation map — canonical source is useKeyboardShortcuts, re-exported for tests
export const APP_SHELL_G_KEY_MAP = G_KEY_MAP;

interface AppShellProps {
  children: ReactNode;
}

export type SystemStatus = "healthy" | "degraded" | "down" | "loading";

/** Pure derivation of system status from useStatus() results. Exported for testing. */
export function deriveSystemStatus(
  data: { nats?: { connected?: boolean }; redis?: { ok?: boolean } } | undefined,
  isError: boolean,
  isLoading: boolean,
): SystemStatus {
  if (isError) return "down";
  if (data) {
    return data.nats?.connected === false || data.redis?.ok === false ? "degraded" : "healthy";
  }
  return isLoading ? "loading" : "degraded";
}

export const statusColorMap: Record<SystemStatus, string> = {
  healthy: "bg-status-healthy",
  degraded: "bg-status-warning",
  loading: "bg-muted-foreground/40",
  down: "bg-status-error",
};

export function AppShell({ children }: AppShellProps) {
  const location = useLocation();
  const navigate = useNavigate();
  const [collapsed, setCollapsed] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const theme = useUiStore((s) => s.resolvedTheme);
  const toggleTheme = useUiStore((s) => s.toggleTheme);
  const user = useConfigStore((s) => s.user);
  const logout = useConfigStore((s) => s.logout);

  // Invalidate worker queries on WebSocket heartbeat events (global listener)
  useWorkerEvents();
  useKeyboardShortcuts();

  const { data: approvalsData } = useApprovals("pending");
  const pendingApprovals = approvalsData?.items?.length ?? 0;
  const { data: dlqData } = useDLQ();
  const dlqCount = dlqData?.items?.length ?? 0;
  const { data: quarantineData } = useQuarantinedJobs();
  const quarantineCount = quarantineData?.items?.length ?? 0;
  const { data: licenseSummary } = useLicense();

  // Accessibility: focus trap for mobile drawer + reduced motion
  const prefersReducedMotion = useReducedMotion();
  const closeMobileMenu = useCallback(() => setMobileOpen(false), []);
  const drawerRef = useDialogA11y(closeMobileMenu, { enabled: mobileOpen });
  const hamburgerRef = useRef<HTMLButtonElement>(null);

  // System health status — derived from GET /status (polled every 10s via useStatus)
  const { data: statusData, isError: statusError, isLoading: statusLoading } = useStatus();
  const systemStatus = deriveSystemStatus(statusData, statusError, statusLoading);
  const statusColor = statusColorMap[systemStatus];

  // Cmd+B / Ctrl+B toggles sidebar collapse
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && (e.key === "b" || e.key === "/")) {
        e.preventDefault();
        setCollapsed((c) => !c);
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);

  // Return focus to hamburger when mobile drawer closes
  const prevMobileOpen = useRef(mobileOpen);
  useEffect(() => {
    if (prevMobileOpen.current && !mobileOpen) {
      hamburgerRef.current?.focus();
    }
    prevMobileOpen.current = mobileOpen;
  }, [mobileOpen]);

  // Close mobile drawer on navigation
  useEffect(() => {
    setMobileOpen(false);
  }, [location.pathname]);

  const getBadgeCount = (badge?: string) => {
    if (badge === "approvals") return pendingApprovals;
    if (badge === "dlq") return dlqCount;
    if (badge === "quarantine") return quarantineCount;
    return 0;
  };

  return (
    <div className="flex h-screen overflow-hidden bg-background">
      {/* Skip-to-main for keyboard navigation — first focusable element */}
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-[100] focus:px-4 focus:py-2 focus:bg-surface-1 focus:border focus:border-accent focus:rounded-xl focus:text-sm focus:font-semibold focus:text-accent focus:shadow-sm"
      >
        Skip to main content
      </a>
      <CommandPalette />
      <KeyboardShortcutsDialog />

      {/* Mobile hamburger */}
      <button type="button"
        ref={hamburgerRef}
        onClick={() => setMobileOpen(true)}
        className="md:hidden fixed top-3 left-3 z-50 p-2 min-w-[44px] min-h-[44px] flex items-center justify-center rounded-md bg-surface-1 border border-border text-muted-foreground hover:text-foreground transition-colors"
        aria-label="Open navigation"
      >
        <Menu className="w-5 h-5" />
      </button>

      {/* Mobile drawer overlay */}
      <AnimatePresence>
        {mobileOpen && (
          <>
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              transition={prefersReducedMotion ? { duration: 0 } : { duration: 0.2 }}
              className="md:hidden fixed inset-0 z-50 bg-[color:var(--surface-glass)] backdrop-blur-md"
              onClick={() => setMobileOpen(false)}
            />
            <motion.aside
              ref={drawerRef as React.Ref<HTMLElement>}
              role="dialog"
              aria-modal="true"
              aria-label="Navigation menu"
              initial={{ x: "-100%" }}
              animate={{ x: 0 }}
              exit={{ x: "-100%" }}
              transition={prefersReducedMotion ? { duration: 0 } : { type: "spring", stiffness: 300, damping: 30 }}
              onAnimationComplete={() => {
                // Focus first interactive element inside drawer after slide-in
                const firstFocusable = drawerRef.current?.querySelector<HTMLElement>("button, a, [tabindex]");
                firstFocusable?.focus();
              }}
              className="md:hidden fixed top-0 left-0 h-screen z-50 w-56 flex flex-col border-r border-border bg-surface-0"
            >
              {/* Close button */}
              <div className="flex items-center justify-between px-4 h-14 border-b border-border shrink-0">
                <div className="flex items-center gap-3">
                  <div className="w-8 h-8 rounded-lg bg-cordum flex items-center justify-center shrink-0">
                    <svg viewBox="0 0 24 24" className="w-5 h-5 text-surface-0" fill="currentColor">
                      <path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" stroke="currentColor" strokeWidth="2" fill="none" strokeLinecap="round" strokeLinejoin="round"/>
                    </svg>
                  </div>
                  <div className="flex flex-col">
                    <span className="font-display font-bold text-sm text-foreground tracking-tight">Cordum</span>
                    <span className="text-xs text-muted-foreground font-mono uppercase tracking-widest">Control Plane</span>
                  </div>
                </div>
                <button type="button"
                  onClick={() => setMobileOpen(false)}
                  className="p-2 min-w-[44px] min-h-[44px] flex items-center justify-center rounded-md text-muted-foreground hover:text-foreground hover:bg-surface-2 transition-colors"
                  aria-label="Close navigation"
                >
                  <X className="w-4 h-4" />
                </button>
              </div>
              {/* Mobile nav items */}
              <nav className="flex-1 py-3 px-2 space-y-4 overflow-y-auto scrollbar-thin">
                {APP_SHELL_NAV_SECTIONS.map((section) => (
                  <div key={section.label}>
                    <p className="px-3 mb-1 text-xs font-semibold uppercase tracking-[0.1em] text-muted-foreground/50">
                      {section.label}
                    </p>
                    <div className="space-y-0.5">
                      {section.items.map((item) => {
                        const badgeCount = getBadgeCount(item.badge);
                        return (
                          <NavLink
                            key={item.path}
                            to={item.path}
                            end={item.end}
                            className={({ isActive }) =>
                              cn(
                                "flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium transition-all duration-150",
                                isActive
                                  ? "bg-cordum/10 text-cordum"
                                  : "text-muted-foreground hover:text-foreground hover:bg-surface-2",
                              )
                            }
                          >
                            <item.icon className="w-4 h-4 shrink-0" />
                            <span className="flex-1">{item.label}</span>
                            {badgeCount > 0 && (
                              <span aria-live="polite" aria-atomic="true" className={cn(
                                "text-xs font-mono font-bold px-1.5 py-0.5 rounded-full",
                                item.badge === "approvals"
                                  ? "bg-status-warning/20 text-status-warning"
                                  : "bg-status-error/20 text-status-error",
                              )}>
                                {badgeCount}
                              </span>
                            )}
                          </NavLink>
                        );
                      })}
                    </div>
                  </div>
                ))}
              </nav>
              {/* Mobile sidebar footer */}
              <div className="px-2 pb-3 border-t border-border pt-3 space-y-1">
                <NavLink
                  to="/settings"
                  className="flex items-center gap-3 px-3 py-2 rounded-md text-sm text-muted-foreground hover:text-foreground hover:bg-surface-2 transition-colors"
                >
                  <Settings className="w-4 h-4 shrink-0" />
                  <span>Settings</span>
                </NavLink>
                <button type="button"
                  onClick={toggleTheme}
                  className="flex items-center gap-3 w-full px-3 py-2 rounded-md text-sm text-muted-foreground hover:text-foreground hover:bg-surface-2 transition-colors"
                >
                  {theme === "dark" ? <Sun className="w-4 h-4 shrink-0" /> : <Moon className="w-4 h-4 shrink-0" />}
                  <span>Toggle theme</span>
                </button>
              </div>
            </motion.aside>
          </>
        )}
      </AnimatePresence>

      {/* Desktop Sidebar */}
      <aside
        className={cn(
          "hidden md:flex fixed top-0 left-0 h-screen z-50 flex-col border-r border-border bg-surface-0 transition-all duration-300",
          collapsed ? "w-16" : "w-56",
        )}
      >
        {/* Logo */}
        <div className="flex items-center gap-3 px-4 h-14 border-b border-border shrink-0">
          <div className="w-8 h-8 rounded-lg bg-cordum flex items-center justify-center shrink-0">
            <svg viewBox="0 0 24 24" className="w-5 h-5 text-surface-0" fill="currentColor">
              <path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" stroke="currentColor" strokeWidth="2" fill="none" strokeLinecap="round" strokeLinejoin="round"/>
            </svg>
          </div>
          {!collapsed && (
            <motion.div
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              className="flex flex-col"
            >
              <span className="font-display font-bold text-sm text-foreground tracking-tight">
                Cordum
              </span>
              <span className="text-xs text-muted-foreground font-mono uppercase tracking-widest">
                Control Plane
              </span>
            </motion.div>
          )}
        </div>

        {/* Nav items */}
        <nav className="flex-1 py-3 px-2 space-y-4 overflow-y-auto scrollbar-thin">
          {APP_SHELL_NAV_SECTIONS.map((section) => (
            <div key={section.label}>
              {!collapsed && (
                <p className="px-3 mb-1 text-xs font-semibold uppercase tracking-[0.1em] text-muted-foreground/50">
                  {section.label}
                </p>
              )}
              {collapsed && (
                <div className="w-6 mx-auto mb-1 border-t border-border/50" />
              )}
              <div className="space-y-0.5">
                {section.items.map((item) => {
                  const badgeCount = getBadgeCount(item.badge);
                  return (
                    <NavLink
                      key={item.path}
                      to={item.path}
                      end={item.end}
                      className={({ isActive }) =>
                        cn(
                          "flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium transition-all duration-150 group relative",
                          isActive
                            ? "bg-cordum/10 text-cordum"
                            : "text-muted-foreground hover:text-foreground hover:bg-surface-2",
                          collapsed && "justify-center px-0",
                        )
                      }
                    >
                      {({ isActive }) => (
                        <>
                          {isActive && (
                            <motion.div
                              layoutId="sidebar-active"
                              className="absolute left-0 top-1/2 -translate-y-1/2 w-[3px] h-5 bg-cordum rounded-r-full"
                              transition={{ type: "spring", stiffness: 350, damping: 30 }}
                            />
                          )}
                          <item.icon className="w-4 h-4 shrink-0" />
                          {!collapsed && (
                            <span className="flex-1">{item.label}</span>
                          )}
                          {!collapsed && badgeCount > 0 && (
                            <span aria-live="polite" aria-atomic="true" className={cn(
                              "text-xs font-mono font-bold px-1.5 py-0.5 rounded-full",
                              item.badge === "approvals"
                                ? "bg-status-warning/20 text-status-warning"
                                : "bg-status-error/20 text-status-error",
                            )}>
                              {badgeCount}
                            </span>
                          )}
                          {collapsed && badgeCount > 0 && (
                            <span className="absolute -top-0.5 -right-0.5 w-2 h-2 rounded-full bg-status-warning" />
                          )}
                        </>
                      )}
                    </NavLink>
                  );
                })}
              </div>
            </div>
          ))}
        </nav>

        {/* Sidebar footer */}
        <div className="px-2 pb-3 border-t border-border pt-3 space-y-1">
          {/* System status */}
          <NavLink
            to="/settings"
            className={cn(
              "flex items-center gap-3 px-3 py-2 rounded-md text-sm text-muted-foreground hover:text-foreground hover:bg-surface-2 transition-colors",
              collapsed && "justify-center px-0",
            )}
          >
            <Settings className="w-4 h-4 shrink-0" />
            {!collapsed && <span>Settings</span>}
          </NavLink>
          <a
            href="https://cordum.io/docs"
            target="_blank"
            rel="noopener noreferrer"
            className={cn(
              "flex items-center gap-3 px-3 py-2 rounded-md text-sm text-muted-foreground hover:text-foreground hover:bg-surface-2 transition-colors",
              collapsed && "justify-center px-0",
            )}
          >
            <ExternalLink className="w-4 h-4 shrink-0" />
            {!collapsed && <span>Docs</span>}
          </a>
          <button type="button"
            onClick={toggleTheme}
            aria-label={collapsed ? (theme === "dark" ? "Switch to light mode" : "Switch to dark mode") : undefined}
            className={cn(
              "flex items-center gap-3 w-full px-3 py-2 rounded-md text-sm text-muted-foreground hover:text-foreground hover:bg-surface-2 transition-colors",
              collapsed && "justify-center px-0",
            )}
          >
            {theme === "dark" ? (
              <Sun className="w-4 h-4 shrink-0" />
            ) : (
              <Moon className="w-4 h-4 shrink-0" />
            )}
            {!collapsed && <span>Toggle theme</span>}
          </button>

          {/* System health indicator + version */}
          {!collapsed && (
            <div className="flex items-center gap-2 px-3 pt-2 mt-1 border-t border-border/50">
              <span className={cn("w-2 h-2 rounded-full shrink-0", statusColor)} />
              <span className="text-xs text-muted-foreground/60 font-mono">
                v0.1.0 · {systemStatus === "loading" ? "loading\u2026" : systemStatus}
              </span>
            </div>
          )}
          {collapsed && (
            <div className="flex justify-center pt-2 mt-1 border-t border-border/50">
              <span className={cn("w-2 h-2 rounded-full", statusColor)} />
            </div>
          )}
        </div>

        {/* Collapse toggle */}
        <button type="button"
          onClick={() => setCollapsed(!collapsed)}
          className="absolute -right-5 top-[72px] w-10 h-10 rounded-full bg-surface-2 border border-border flex items-center justify-center text-muted-foreground hover:text-foreground hover:bg-surface-3 transition-colors"
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
        >
          {collapsed ? (
            <ChevronRight className="w-3.5 h-3.5" />
          ) : (
            <ChevronLeft className="w-3.5 h-3.5" />
          )}
        </button>
      </aside>

      {/* Main content area */}
      <div
        className={cn(
          "flex-1 flex flex-col overflow-hidden transition-all duration-300",
          collapsed ? "ml-0 md:ml-16" : "ml-0 md:ml-56",
        )}
      >
        {/* Top bar */}
        <header className="sticky top-0 z-40 flex items-center justify-between h-12 px-6 border-b border-border bg-background/80 backdrop-blur-xl shrink-0">
          <div className="flex items-center gap-4">
            <button type="button"
              onClick={() => {
                window.dispatchEvent(new KeyboardEvent("keydown", { key: "k", metaKey: true, bubbles: true }));
              }}
              className="relative flex items-center h-8 w-56 pl-8 pr-3 text-xs bg-surface-1 border border-border rounded-md text-muted-foreground hover:border-cordum/30 transition-colors"
            >
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5" />
              <span>Search...</span>
              <kbd className="absolute right-2 top-1/2 -translate-y-1/2 text-xs font-mono px-1.5 py-0.5 rounded bg-background border border-border">
                <Command className="w-2.5 h-2.5 inline" />K
              </kbd>
            </button>
          </div>
          <div className="flex items-center gap-2">
            {licenseSummary?.plan && (
              <button
                type="button"
                onClick={() => navigate("/settings/license")}
                className="hidden rounded-full md:block"
                title="View current plan and limits"
                aria-label="Open license and limits page"
              >
                <TierBadge plan={licenseSummary.plan} className="hover:opacity-90" />
              </button>
            )}
            <ConnectionIndicator />

            {/* Pending approvals badge in top bar */}
            {pendingApprovals > 0 && (
              <button type="button"
                onClick={() => navigate("/approvals")}
                className="flex items-center gap-1.5 h-7 px-2.5 rounded-md bg-status-warning/10 border border-status-warning/20 text-status-warning text-xs font-medium hover:bg-status-warning/20 transition-colors"
              >
                <UserCheck className="w-3.5 h-3.5" />
                <span className="font-mono">{pendingApprovals}</span>
                <span className="hidden sm:inline">pending</span>
              </button>
            )}

            <NotificationPopover />

            {/* User */}
            {user ? (
              <div className="flex items-center gap-2 pl-2 border-l border-border">
                <div className="w-7 h-7 rounded-full bg-cordum/20 border border-cordum/30 flex items-center justify-center">
                  <span className="text-xs font-semibold text-cordum">
                    {(user.display_name || user.username || "U").charAt(0).toUpperCase()}
                  </span>
                </div>
                <button type="button"
                  onClick={logout}
                  className="p-2 min-w-[44px] min-h-[44px] flex items-center justify-center rounded-md text-muted-foreground hover:text-destructive transition-colors"
                  title="Logout"
                  aria-label="Sign out"
                >
                  <LogOut className="w-3.5 h-3.5" />
                </button>
              </div>
            ) : (
              <div className="w-7 h-7 rounded-full bg-cordum/20 border border-cordum/30 flex items-center justify-center">
                <span className="text-xs font-semibold text-cordum">C</span>
              </div>
            )}
          </div>
        </header>

        {/* Telemetry consent banner */}
        <TelemetryConsentBanner />

        {/* Page content */}
        <main id="main-content" className="flex-1 overflow-y-auto dot-grid">
          <motion.div
            key={location.pathname}
            initial={{ opacity: 0, y: 6 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.15, ease: "easeOut" }}
            className="p-6"
          >
            {children}
          </motion.div>
        </main>
      </div>
    </div>
  );
}
