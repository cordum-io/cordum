import { useState, useEffect, useCallback, useRef, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { motion, AnimatePresence } from "framer-motion";
import { useDialogA11y } from "@/hooks/useDialogA11y";
import {
  LayoutGrid, ListChecks, Workflow, Cpu, UserCheck, Shield, ShieldCheck, ShieldAlert, Boxes,
  AlertTriangle, FileText, Settings, Search, Activity, Key, Bell,
  Users, Server, Globe, Monitor, ArrowRight, GitBranch, Hash,
  History, TrendingUp, Zap, Layers,
} from "lucide-react";

interface CommandItem {
  id: string;
  label: string;
  section: string;
  icon: React.ElementType;
  path: string;
  keywords?: string[];
}

/** @internal exported for unit tests */
export const COMMAND_PALETTE_COMMANDS: CommandItem[] = [
  { id: "home", label: "Dashboard Overview", section: "Navigate", icon: LayoutGrid, path: "/", keywords: ["home", "overview", "dashboard"] },
  { id: "jobs", label: "Jobs", section: "Navigate", icon: ListChecks, path: "/jobs", keywords: ["jobs", "tasks", "queue"] },
  { id: "workflows", label: "Workflows", section: "Navigate", icon: Workflow, path: "/workflows", keywords: ["workflows", "orchestration", "pipeline"] },
  { id: "agents", label: "Agent Fleet", section: "Navigate", icon: Cpu, path: "/agents", keywords: ["agents", "workers", "fleet", "pool"] },
  { id: "approvals", label: "Approvals", section: "Navigate", icon: UserCheck, path: "/approvals", keywords: ["approvals", "pending", "approve", "deny"] },
  { id: "security", label: "Security Overview", section: "Navigate", icon: ShieldCheck, path: "/", keywords: ["security", "overview", "safety", "decisions"] },
  { id: "policy-studio", label: "Policy Studio", section: "Govern", icon: Shield, path: "/govern/overview", keywords: ["policy studio", "governance", "rules", "bundles", "evaluation", "scope"] },
  { id: "input-rules", label: "Input Rules", section: "Govern", icon: Shield, path: "/govern/overview?tab=input-rules", keywords: ["input", "rules", "safety", "pii", "injection", "policies", "governance"] },
  { id: "output-rules", label: "Output Rules", section: "Govern", icon: Shield, path: "/govern/overview?tab=output-rules", keywords: ["output", "rules", "safety", "quarantine", "policies"] },
  { id: "velocity", label: "Velocity Controls", section: "Govern", icon: Zap, path: "/govern/overview?tab=velocity", keywords: ["velocity", "rate limit", "thresholds", "approval fatigue"] },
  { id: "bundles", label: "Bundles", section: "Govern", icon: GitBranch, path: "/govern/overview?tab=bundles", keywords: ["bundles", "policy", "publish", "deploy", "history", "changelog", "versions"] },
  { id: "analytics", label: "Rule Quality Analytics", section: "Govern", icon: TrendingUp, path: "/govern/overview?tab=evaluation&mode=analytics", keywords: ["analytics", "evaluation", "overrides", "fatigue", "rule quality"] },
  { id: "replay", label: "Replay & Compare", section: "Govern", icon: History, path: "/govern/overview?tab=evaluation&mode=replay", keywords: ["replay", "compare", "candidate policy", "what if", "historical"] },
  { id: "simulator", label: "Simulator", section: "Govern", icon: Shield, path: "/govern/overview?tab=evaluation&mode=simulator", keywords: ["simulator", "test", "dry run", "evaluation"] },
  { id: "scope", label: "Scope & Rollout", section: "Govern", icon: Layers, path: "/govern/overview?tab=scope", keywords: ["scope", "tenants", "hierarchy", "inheritance", "multi-tenant", "rollout"] },
  { id: "quarantine", label: "Quarantine", section: "Govern", icon: ShieldAlert, path: "/govern/quarantine", keywords: ["quarantine", "output", "blocked", "review"] },
  { id: "packs", label: "Packs", section: "Navigate", icon: Boxes, path: "/packs", keywords: ["packs", "marketplace", "plugins"] },
  { id: "topics", label: "Topics", section: "Navigate", icon: Hash, path: "/topics", keywords: ["topics", "registry", "routing", "pool mappings"] },
  { id: "schemas", label: "Schemas", section: "Navigate", icon: Monitor, path: "/schemas", keywords: ["schemas", "types", "definitions"] },
  { id: "dlq", label: "Dead Letter Queue", section: "Navigate", icon: AlertTriangle, path: "/dlq", keywords: ["dlq", "dead letter", "failed", "retry"] },
  { id: "audit", label: "Audit Log", section: "Navigate", icon: FileText, path: "/audit", keywords: ["audit", "log", "events", "history"] },
  { id: "settings", label: "Settings Hub", section: "Settings", icon: Settings, path: "/settings", keywords: ["settings", "config"] },
  { id: "settings-config", label: "System Config", section: "Settings", icon: Settings, path: "/settings/config", keywords: ["config", "configuration", "system"] },
  { id: "settings-env", label: "Environments", section: "Settings", icon: Globe, path: "/settings/environments", keywords: ["environments", "production", "staging"] },
  { id: "settings-health", label: "System Health", section: "Settings", icon: Activity, path: "/settings/health", keywords: ["health", "diagnostics", "status"] },
  { id: "settings-keys", label: "API Keys", section: "Settings", icon: Key, path: "/settings/keys", keywords: ["api", "keys", "tokens"] },
  { id: "settings-mcp", label: "MCP Server", section: "Settings", icon: Server, path: "/settings/mcp", keywords: ["mcp", "tools", "model context"] },
  { id: "settings-notif", label: "Notifications", section: "Settings", icon: Bell, path: "/settings/notifications", keywords: ["notifications", "alerts", "channels"] },
  { id: "settings-users", label: "Users & RBAC", section: "Settings", icon: Users, path: "/settings/users", keywords: ["users", "roles", "permissions", "rbac"] },
];

export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  const dialogRef = useDialogA11y(() => setOpen(false), { enabled: open });

  // Cmd+K to open
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setOpen((prev) => !prev);
      }
      if (e.key === "Escape") {
        setOpen(false);
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);

  // Focus input when opened
  useEffect(() => {
    if (open) {
      setQuery("");
      setSelectedIndex(0);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [open]);

  const filtered = useMemo(() => {
    if (!query.trim()) return COMMAND_PALETTE_COMMANDS;
    const q = query.toLowerCase();
    return COMMAND_PALETTE_COMMANDS.filter(
      (c) =>
        c.label.toLowerCase().includes(q) ||
        c.section.toLowerCase().includes(q) ||
        c.keywords?.some((k) => k.includes(q))
    );
  }, [query]);

  const grouped = useMemo(() => {
    const groups: Record<string, CommandItem[]> = {};
    filtered.forEach((item) => {
      if (!groups[item.section]) groups[item.section] = [];
      groups[item.section].push(item);
    });
    return groups;
  }, [filtered]);

  const flatItems = useMemo(() => filtered, [filtered]);

  const handleSelect = useCallback(
    (item: CommandItem) => {
      navigate(item.path);
      setOpen(false);
    },
    [navigate]
  );

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSelectedIndex((i) => Math.min(i + 1, flatItems.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setSelectedIndex((i) => Math.max(i - 1, 0));
    } else if (e.key === "Enter" && flatItems[selectedIndex]) {
      handleSelect(flatItems[selectedIndex]);
    }
  };

  // Scroll selected item into view
  useEffect(() => {
    const el = listRef.current?.querySelector(`[data-index="${selectedIndex}"]`);
    el?.scrollIntoView({ block: "nearest" });
  }, [selectedIndex]);

  return (
    <AnimatePresence>
      {open && (
        <>
          {/* Backdrop */}
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.15 }}
            className="fixed inset-0 z-[100] bg-black/60 backdrop-blur-sm"
            onClick={() => setOpen(false)}
          />
          {/* Palette */}
          <motion.div
            initial={{ opacity: 0, scale: 0.96, y: -10 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.96, y: -10 }}
            transition={{ duration: 0.15, ease: "easeOut" }}
            className="fixed top-[20%] left-1/2 -translate-x-1/2 z-[101] w-full max-w-lg"
          >
            <div
              ref={dialogRef}
              role="dialog"
              aria-modal="true"
              aria-label="Command palette"
              className="bg-surface-1 border border-border rounded-xl shadow-2xl overflow-hidden"
            >
              {/* Search input */}
              <div className="flex items-center gap-3 px-4 h-14 border-b border-border">
                <Search className="w-4 h-4 text-muted-foreground shrink-0" />
                <input
                  ref={inputRef}
                  type="text"
                  value={query}
                  onChange={(e) => {
                    setQuery(e.target.value);
                    setSelectedIndex(0);
                  }}
                  onKeyDown={handleKeyDown}
                  placeholder="Type a command or search..."
                  className="flex-1 bg-transparent text-sm text-foreground placeholder:text-muted-foreground focus:outline-none"
                />
                <kbd className="text-xs font-mono px-1.5 py-0.5 rounded bg-surface-2 border border-border text-muted-foreground">
                  ESC
                </kbd>
              </div>

              {/* Results */}
              <div ref={listRef} className="max-h-80 overflow-y-auto py-2">
                {flatItems.length === 0 ? (
                  <div className="px-4 py-8 text-center text-sm text-muted-foreground">
                    No results found for "{query}"
                  </div>
                ) : (
                  Object.entries(grouped).map(([section, items]) => (
                    <div key={section}>
                      <p className="px-4 pt-2 pb-1 text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground/60">
                        {section}
                      </p>
                      {items.map((item) => {
                        const globalIndex = flatItems.indexOf(item);
                        return (
                          <button type="button"
                            key={item.id}
                            data-index={globalIndex}
                            onClick={() => handleSelect(item)}
                            onMouseEnter={() => setSelectedIndex(globalIndex)}
                            className={`w-full flex items-center gap-3 px-4 py-2.5 text-sm transition-colors ${
                              globalIndex === selectedIndex
                                ? "bg-cordum/10 text-cordum"
                                : "text-foreground hover:bg-surface-2"
                            }`}
                          >
                            <item.icon className="w-4 h-4 shrink-0 opacity-60" />
                            <span className="flex-1 text-left">{item.label}</span>
                            {globalIndex === selectedIndex && (
                              <ArrowRight className="w-3.5 h-3.5 opacity-40" />
                            )}
                          </button>
                        );
                      })}
                    </div>
                  ))
                )}
              </div>

              {/* Footer */}
              <div className="flex items-center gap-4 px-4 py-2 border-t border-border text-xs text-muted-foreground">
                <span className="flex items-center gap-1">
                  <kbd className="px-1 py-0.5 rounded bg-surface-2 border border-border font-mono">↑↓</kbd>
                  navigate
                </span>
                <span className="flex items-center gap-1">
                  <kbd className="px-1 py-0.5 rounded bg-surface-2 border border-border font-mono">↵</kbd>
                  select
                </span>
                <span className="flex items-center gap-1">
                  <kbd className="px-1 py-0.5 rounded bg-surface-2 border border-border font-mono">esc</kbd>
                  close
                </span>
              </div>
            </div>
          </motion.div>
        </>
      )}
    </AnimatePresence>
  );
}
