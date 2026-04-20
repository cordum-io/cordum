import { NavLink, useLocation, useNavigate } from "react-router-dom";
import { type ReactNode, useState, useEffect, useRef } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { cn } from "@/lib/utils";
import { useConfigStore } from "@/state/config";
import { useUiStore } from "@/state/ui";
import { useApprovals } from "@/hooks/useApprovals";
import { useDLQ } from "@/hooks/useDLQ";
import { useQuarantinedJobs } from "@/hooks/useOutputPolicy";
import { useStatus } from "@/hooks/useStatus";
import { useWorkerEvents } from "@/hooks/useWorkers";
import { CommandPalette } from "@/components/CommandPalette";
import { NotificationPopover } from "@/components/NotificationPopover";
import { ConnectionIndicator } from "@/components/ConnectionIndicator";
import { KeyboardShortcutsDialog } from "@/components/KeyboardShortcuts";
import {
  LayoutGrid,
  ListChecks,
  Workflow,
  Cpu,
  UserCheck,
  Shield,
  Boxes,
  AlertTriangle,
  FileText,
  Settings,
  ChevronLeft,
  ChevronRight,
  Moon,
  Network,
  Play,
  Search,
  Command,
  ExternalLink,
  ShieldCheck,
  ShieldAlert,
  GitBranch,
  Activity,
  Package,
  Database,
  Eye,
  Layers,
  Zap,
  Menu,
  X,
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

const navSections: NavSection[] = [
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
      { path: "/policies", label: "Policy Studio", icon: Shield },
      { path: "/quarantine", label: "Quarantine", icon: ShieldAlert, badge: "quarantine" },
    ],
  },
  {
    label: "Extend",
    items: [
      { path: "/packs", label: "Packs", icon: Package },
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

// g+key navigation map
const gKeyMap: Record<string, string> = {
  h: "/",
  j: "/jobs",
  w: "/workflows",
  a: "/agents",
  p: "/policies",
  s: "/settings",
  d: "/dlq",
  l: "/audit",
  t: "/traces",
  b: "/policies/bundles",
};

interface AppShellProps {
  children: ReactNode;
}

export function AppShell({ children }: AppShellProps) {
  const location = useLocation();
  const navigate = useNavigate();
  const [collapsed, setCollapsed] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const theme = useUiStore((s) => s.resolvedTheme);
  const toggleTheme = useUiStore((s) => s.toggleTheme);
  const user = useConfigStore((s) => s.user);
  const logout = useConfigStore((s) => s.logout);
  const gPressedRef = useRef(false);
  const gTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);

  // Invalidate worker queries on WebSocket heartbeat events (global listener)
  useWorkerEvents();

  const { data: approvalsData } = useApprovals("pending");
  const pendingApprovals = approvalsData?.items?.length ?? 0;
  const { data: dlqData } = useDLQ();
  const dlqCount = dlqData?.items?.length ?? 0;
  const { data: quarantineData } = useQuarantinedJobs();
  const quarantineCount = quarantineData?.items?.length ?? 0;

  // System health status — derived from GET /status (polled every 10s via useStatus)
  const { data: statusData, isError: statusError } = useStatus();
  const systemStatus: "healthy" | "degraded" | "down" = statusError
    ? "down"
    : statusData
      ? (statusData.nats?.connected === false || statusData.redis?.ok === false ? "degraded" : "healthy")
      : "healthy";
  const statusColor = systemStatus === "healthy" ? "bg-status-healthy" : systemStatus === "degraded" ? "bg-status-warning" : "bg-status-error";

  // Keyboard shortcuts: Cmd+B sidebar, g+key navigation
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement;
      const isInput = target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable;

      if ((e.metaKey || e.ctrlKey) && (e.key === "b" || e.key === "/")) {
        e.preventDefault();
        setCollapsed((c) => !c);
        return;
      }

      if (!isInput && !e.metaKey && !e.ctrlKey && !e.altKey) {
        if (e.key === "g") {
          gPressedRef.current = true;
          clearTimeout(gTimerRef.current);
          gTimerRef.current = setTimeout(() => {
            gPressedRef.current = false;
          }, 500);
          return;
        }
        if (gPressedRef.current && gKeyMap[e.key]) {
          e.preventDefault();
          navigate(gKeyMap[e.key]);
          gPressedRef.current = false;
          clearTimeout(gTimerRef.current);
          return;
        }
      }
    };
    window.addEventListener("keydown", handler);
    return () => {
      window.removeEventListener("keydown", handler);
      clearTimeout(gTimerRef.current);
    };
  }, [navigate]);

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
      <CommandPalette />
      <KeyboardShortcutsDialog />

      {/* Mobile hamburger */}
      <button
        onClick={() => setMobileOpen(true)}
        className="md:hidden fixed top-3 left-3 z-50 p-2 rounded-md bg-surface-1 border border-border text-muted-foreground hover:text-foreground transition-colors"
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
              transition={{ duration: 0.2 }}
              className="md:hidden fixed inset-0 z-50 bg-black/50 backdrop-blur-sm"
              onClick={() => setMobileOpen(false)}
            />
            <motion.aside
              initial={{ x: "-100%" }}
              animate={{ x: 0 }}
              exit={{ x: "-100%" }}
              transition={{ type: "spring", stiffness: 300, damping: 30 }}
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
                    <span className="text-[10px] text-muted-foreground font-mono uppercase tracking-widest">Control Plane</span>
                  </div>
                </div>
                <button
                  onClick={() => setMobileOpen(false)}
                  className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-surface-2 transition-colors"
                  aria-label="Close navigation"
                >
                  <X className="w-4 h-4" />
                </button>
              </div>
              {/* Mobile nav items */}
              <nav className="flex-1 py-3 px-2 space-y-4 overflow-y-auto scrollbar-thin">
                {navSections.map((section) => (
                  <div key={section.label}>
                    <p className="px-3 mb-1 text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground/50">
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
                                "flex items-center gap-3 px-3 py-2 rounded-md text-[13px] font-medium transition-all duration-150",
                                isActive
                                  ? "bg-cordum/10 text-cordum"
                                  : "text-muted-foreground hover:text-foreground hover:bg-surface-2",
                              )
                            }
                          >
                            <item.icon className="w-4 h-4 shrink-0" />
                            <span className="flex-1">{item.label}</span>
                            {badgeCount > 0 && (
                              <span className={cn(
                                "text-[10px] font-mono font-bold px-1.5 py-0.5 rounded-full",
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
                  className="flex items-center gap-3 px-3 py-2 rounded-md text-[13px] text-muted-foreground hover:text-foreground hover:bg-surface-2 transition-colors"
                >
                  <Settings className="w-4 h-4 shrink-0" />
                  <span>Settings</span>
                </NavLink>
                <button
                  onClick={toggleTheme}
                  className="flex items-center gap-3 w-full px-3 py-2 rounded-md text-[13px] text-muted-foreground hover:text-foreground hover:bg-surface-2 transition-colors"
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
              <span className="text-[10px] text-muted-foreground font-mono uppercase tracking-widest">
                Control Plane
              </span>
            </motion.div>
          )}
        </div>

        {/* Nav items */}
        <nav className="flex-1 py-3 px-2 space-y-4 overflow-y-auto scrollbar-thin">
          {navSections.map((section) => (
            <div key={section.label}>
              {!collapsed && (
                <p className="px-3 mb-1 text-[10px] font-semibold uppercase tracking-[0.1em] text-muted-foreground/50">
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
                          "flex items-center gap-3 px-3 py-2 rounded-md text-[13px] font-medium transition-all duration-150 group relative",
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
                            <span className={cn(
                              "text-[10px] font-mono font-bold px-1.5 py-0.5 rounded-full",
                              item.badge === "approvals"
                                ? "bg-status-warning/20 text-status-warning"
                                : "bg-status-error/20 text-status-error",
                            )}>
                              {badgeCount}
                            </span>
                          )}
                        </div>
                        <ChevronRight
                          className={cn("h-3 w-3 transition-transform", isExpanded && "rotate-90")}
                        />
                      </button>
                      <div
                        id={`sidebar-section-${group.title.toLowerCase()}`}
                        className={cn(
                          "flex flex-col gap-1 overflow-hidden transition-all",
                          isExpanded ? "max-h-[500px] opacity-100" : "max-h-0 opacity-0"
                        )}
                      >
                        {group.items.map((item) => {
                          const Icon = item.icon;
                          const badge = navBadges[item.path];
                          const badgeText = badge && badge.count > 0 ? formatCount(badge.count) : "";
                          return (
                            <NavLink
                              key={item.path}
                              to={item.path}
                              className={({ isActive }) =>
                                cn(
                                  "group relative flex h-10 items-center gap-3 rounded-md border px-3 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35",
                                  isActive
                                    ? "border-status-info-border bg-status-info-bg text-foreground"
                                    : "border-transparent text-secondary-foreground hover:border-border hover:bg-surface-2/60 hover:text-foreground"
                                )
                              }
                            >
                              {({ isActive }) => (
                                <>
                                  {isActive ? <span aria-hidden className="absolute left-1 h-5 w-[2px] rounded-full bg-accent" /> : null}
                              <Icon className="h-4 w-4 shrink-0" />
                              <span className="truncate">{item.label}</span>
                              {badgeText ? (
                                <span
                                  className={cn(
                                    "ml-auto rounded-sm border px-1.5 py-0.5 text-[10px] font-semibold",
                                    badge.variant === "danger"
                                      ? "border-status-danger-border bg-status-danger-bg text-danger"
                                      : "border-status-warning-border bg-status-warning-bg text-warning"
                                  )}
                                >
                                  {badgeText}
                                </span>
                              ) : null}
                                </>
                              )}
                            </NavLink>
                          );
                        })}
                      </div>
                    </div>
                  );
                })}
            </div>
            {/* Settings Pinned at Bottom */}
            <div className="mt-auto border-t border-border pt-3">
              {navGroups
                .filter((group) => group.title === "SETTINGS")
                .map((group) => (
                  <div key={group.title} className="flex flex-col gap-1">
                    <div className="px-4 py-1 text-[11px] font-bold uppercase tracking-[0.2em] text-muted">
                      {group.title}
                    </div>
                    {group.items.map((item) => {
                      const Icon = item.icon;
                      return (
                        <NavLink
                          key={item.path}
                          to={item.path}
                          className={({ isActive }) =>
                            cn(
                              "group relative flex h-10 items-center gap-3 rounded-md border px-3 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35",
                              isActive
                                ? "border-status-info-border bg-status-info-bg text-foreground"
                                : "border-transparent text-secondary-foreground hover:border-border hover:bg-surface-2/60 hover:text-foreground"
                            )
                          }
                        >
                          {({ isActive }) => (
                            <>
                              {isActive ? <span aria-hidden className="absolute left-1 h-5 w-[2px] rounded-full bg-accent" /> : null}
                              <Icon className="h-4 w-4" />
                              {item.label}
                            </>
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
              "flex items-center gap-3 px-3 py-2 rounded-md text-[13px] text-muted-foreground hover:text-foreground hover:bg-surface-2 transition-colors",
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
              "flex items-center gap-3 px-3 py-2 rounded-md text-[13px] text-muted-foreground hover:text-foreground hover:bg-surface-2 transition-colors",
              collapsed && "justify-center px-0",
            )}
          >
            <ExternalLink className="w-4 h-4 shrink-0" />
            {!collapsed && <span>Docs</span>}
          </a>
          <button
            onClick={toggleTheme}
            className={cn(
              "flex items-center gap-3 w-full px-3 py-2 rounded-md text-[13px] text-muted-foreground hover:text-foreground hover:bg-surface-2 transition-colors",
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
              <span className="text-[10px] text-muted-foreground/60 font-mono">
                v0.1.0 · {systemStatus}
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
        <button
          onClick={() => setCollapsed(!collapsed)}
          className="absolute -right-3 top-20 w-6 h-6 rounded-full bg-surface-2 border border-border flex items-center justify-center text-muted-foreground hover:text-foreground hover:bg-surface-3 transition-colors"
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
            <button
              onClick={() => {
                window.dispatchEvent(new KeyboardEvent("keydown", { key: "k", metaKey: true, bubbles: true }));
              }}
              className="relative flex items-center h-8 w-56 pl-8 pr-3 text-xs bg-surface-1 border border-border rounded-md text-muted-foreground hover:border-cordum/30 transition-colors"
            >
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5" />
              <span>Search...</span>
              <kbd className="absolute right-2 top-1/2 -translate-y-1/2 text-[10px] font-mono px-1.5 py-0.5 rounded bg-background border border-border">
                <Command className="w-2.5 h-2.5 inline" />K
              </kbd>
            </button>
          </div>
          <div className="flex items-center gap-2">
            <ConnectionIndicator />

            {/* Pending approvals badge in top bar */}
            {pendingApprovals > 0 && (
              <button
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
                  <span className="text-[11px] font-semibold text-cordum">
                    {(user.display_name || user.username || "U").charAt(0).toUpperCase()}
                  </span>
                </div>
              </div>
              <div className="flex flex-1 flex-col gap-3 lg:flex-row lg:items-center lg:justify-end">
                <div className="relative flex-1 lg:max-w-sm">
                  <Search
                    className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
                    aria-hidden
                  />
                  <Input
                    value={globalSearch}
                    onChange={(event) => setGlobalSearch(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") {
                        const next = event.currentTarget.value.trim();
                        setCommandOpen(true, next);
                      }
                    }}
                    placeholder="Search runs, workflows, packs, jobs..."
                    aria-label="Search dashboard resources"
                    className="bg-surface-1/70 pl-9"
                  />
                </div>
                <Button
                  variant="outline"
                  size="control"
                  type="button"
                  onClick={toggleTheme}
                  aria-label={`Toggle theme mode (current: ${theme})`}
                >
                  {theme === "light" && <Sun className="h-4 w-4" />}
                  {theme === "dark" && <Moon className="h-4 w-4" />}
                  {theme === "system" && <Monitor className="h-4 w-4" />}
                  {theme === "light" ? "Light" : theme === "dark" ? "Dark" : "System"}
                </Button>
                <Button
                  variant="outline"
                  size="control"
                  onClick={() => setCommandOpen(true)}
                  type="button"
                  aria-label="Open command palette"
                >
                  <LayoutGrid className="h-4 w-4" />
                  Command
                </Button>
                {requiresAuth && apiKey ? (
                  <div className="flex items-center gap-2">
                    <div className="flex items-center gap-2 rounded-md border border-border bg-surface-1/70 px-2.5 py-1.5 text-xs text-foreground">
                      <UserCircle className="h-4 w-4" />
                      <div className="leading-tight">
                        <div className="text-xs font-semibold">{displayName}</div>
                        <div className="text-[10px] text-muted-foreground">
                          {tenantLabel}
                          {roleLabel ? ` · ${roleLabel}` : ""}
                        </div>
                      </div>
                    </div>
                    <Button
                      variant="outline"
                      size="control"
                      type="button"
                      onClick={onLogout}
                      disabled={loggingOut}
                      aria-label={loggingOut ? "Logging out" : "Logout"}
                    >
                      <LogOut className="h-4 w-4" />
                      {loggingOut ? "Logging out" : "Logout"}
                    </Button>
                  </div>
                ) : null}
              </div>
            </div>
            <nav className="mt-4 flex gap-2 overflow-x-auto pb-2 lg:hidden">
              {navGroups.map((group, gIdx) => (
                <div key={group.title} className="flex gap-2 shrink-0">
                  {gIdx > 0 && <div className="mx-1 h-8 w-px shrink-0 self-center bg-border" />}
                  {group.items.map((item) => {
                    const Icon = item.icon;
                    const badge = navBadges[item.path];
                    const badgeText = badge && badge.count > 0 ? formatCount(badge.count) : "";
                    return (
                      <NavLink
                        key={item.path}
                        to={item.path}
                        className={({ isActive }) =>
                          cn(
                            "flex h-9 items-center gap-2 rounded-md border px-3 text-xs font-semibold uppercase tracking-[0.14em] whitespace-nowrap transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35",
                            isActive
                              ? "border-status-info-border bg-status-info-bg text-foreground"
                              : "border-border bg-surface-1/70 text-secondary-foreground hover:bg-surface-2/70 hover:text-foreground"
                          )
                        }
                      >
                        <Icon className="h-3 w-3" />
                        {item.label}
                        {badgeText ? (
                          <span
                            className={cn(
                              "rounded-sm border px-1.5 py-0.5 text-[10px] font-semibold",
                              badge.variant === "danger"
                                ? "border-status-danger-border bg-status-danger-bg text-danger"
                                : "border-status-warning-border bg-status-warning-bg text-warning"
                            )}
                          >
                            {badgeText}
                          </span>
                        ) : null}
                      </NavLink>
                    );
                  })}
                </div>
              ))}
            </nav>
          </header>
          <MaintenanceBanner />
          <main className="flex-1 px-4 py-6 lg:px-8">{children}</main>
        </div>
      </div>
    </div>
  );
}
