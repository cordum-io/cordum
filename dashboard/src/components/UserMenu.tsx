import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { motion, AnimatePresence } from "framer-motion";
import { LogOut, Settings as SettingsIcon, Shield } from "lucide-react";
import { useConfigStore } from "@/state/config";
import { useDialogA11y } from "@/hooks/useDialogA11y";

function initialOf(...candidates: Array<string | undefined>): string {
  for (const c of candidates) {
    if (typeof c === "string" && c.length > 0) {
      return c.charAt(0).toUpperCase();
    }
  }
  return "U";
}

export function UserMenu() {
  const navigate = useNavigate();
  const user = useConfigStore((s) => s.user);
  const logout = useConfigStore((s) => s.logout);
  const principalRole = useConfigStore((s) => s.principalRole);

  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const dialogRef = useDialogA11y(() => setOpen(false), { enabled: open });

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    if (open) document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  if (!user) {
    return (
      <div className="w-7 h-7 rounded-full bg-cordum/20 border border-cordum/30 flex items-center justify-center">
        <span className="text-xs font-semibold text-cordum">C</span>
      </div>
    );
  }

  const initial = initialOf(user.display_name, user.username);
  const role =
    user.roles?.[0] ?? (principalRole !== "" ? principalRole : "viewer");
  const isAdmin = role === "admin";
  const tenant = user.tenant || "default";

  const goAndClose = (path: string) => {
    setOpen(false);
    navigate(path);
  };

  const onLogout = () => {
    setOpen(false);
    logout();
  };

  return (
    <div ref={containerRef} className="relative pl-2 border-l border-border">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Account menu for ${user.display_name || user.username}`}
        className="flex items-center gap-2 p-1 rounded-xl hover:bg-surface-2 transition-colors min-w-[44px] min-h-[44px]"
      >
        <div className="w-7 h-7 rounded-full bg-cordum/20 border border-cordum/30 flex items-center justify-center">
          <span className="text-xs font-semibold text-cordum">{initial}</span>
        </div>
      </button>

      <AnimatePresence>
        {open && (
          <motion.div
            ref={dialogRef}
            initial={{ opacity: 0, y: 4, scale: 0.97 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 4, scale: 0.97 }}
            transition={{ duration: 0.15 }}
            role="menu"
            aria-label="Account menu"
            className="absolute right-0 top-full mt-2 w-72 bg-surface-1 border border-border rounded-xl shadow-2xl overflow-hidden z-[90]"
          >
            <div className="px-4 py-3 border-b border-border">
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-full bg-cordum/20 border border-cordum/30 flex items-center justify-center shrink-0">
                  <span className="text-base font-semibold text-cordum">{initial}</span>
                </div>
                <div className="min-w-0">
                  <p className="text-sm font-display font-semibold text-foreground truncate">
                    {user.display_name || user.username}
                  </p>
                  {user.email && (
                    <p className="text-xs text-muted-foreground truncate">{user.email}</p>
                  )}
                </div>
              </div>
              <div className="flex items-center gap-1.5 mt-3">
                <span
                  className={`inline-flex items-center gap-1 h-5 px-2 rounded-full text-[10px] font-mono uppercase tracking-wider border ${
                    isAdmin
                      ? "bg-cordum/15 border-cordum/40 text-cordum"
                      : "bg-surface-2 border-border text-muted-foreground"
                  }`}
                >
                  {isAdmin && <Shield className="w-3 h-3" />}
                  {role}
                </span>
                <span className="inline-flex items-center h-5 px-2 rounded-full text-[10px] font-mono bg-surface-2 border border-border text-muted-foreground">
                  {tenant}
                </span>
              </div>
            </div>

            <div className="py-1">
              <button
                type="button"
                role="menuitem"
                onClick={() => goAndClose("/settings")}
                className="w-full flex items-center gap-3 px-4 py-2 text-sm text-foreground hover:bg-surface-2 transition-colors"
              >
                <SettingsIcon className="w-4 h-4 text-muted-foreground" />
                Settings
              </button>
              {isAdmin && (
                <button
                  type="button"
                  role="menuitem"
                  onClick={() => goAndClose("/govern/verification")}
                  className="w-full flex items-center gap-3 px-4 py-2 text-sm text-foreground hover:bg-surface-2 transition-colors"
                >
                  <Shield className="w-4 h-4 text-muted-foreground" />
                  Chain verification
                </button>
              )}
            </div>

            <div className="border-t border-border py-1">
              <button
                type="button"
                role="menuitem"
                onClick={onLogout}
                className="w-full flex items-center gap-3 px-4 py-2 text-sm text-destructive hover:bg-destructive/10 transition-colors"
              >
                <LogOut className="w-4 h-4" />
                Sign out
              </button>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
