import { useState, useCallback } from "react";
import { Outlet, NavLink } from "react-router-dom";
import { Shield, Layers, Blocks, Play, History, BarChart3, Lock, Unlock } from "lucide-react";
import { cn } from "../../lib/utils";
import { POLICY_CONFIG_SUPPORTED, POLICY_STATS_SUPPORTED, usePolicyConfig, useDeactivateLockdown } from "../../hooks/usePolicies";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { PolicyBundleProvider, usePolicyBundleContext } from "./PolicyBundleContext";

const BASE_TABS = [
  { to: "/policies", label: "Overview", icon: Layers, end: true },
  { to: "/policies/rules", label: "Rules", icon: Shield, end: false },
  { to: "/policies/rules/new", label: "Editor", icon: Blocks, end: false },
  { to: "/policies/simulator", label: "Simulator", icon: Play, end: false },
  { to: "/policies/history", label: "History", icon: History, end: false },
  { to: "/policies/analytics", label: "Analytics", icon: BarChart3, end: false },
] as const;

function PolicyLayoutInner() {
  const { bundleId, setBundleId, bundles } = usePolicyBundleContext();
  const { data: config } = usePolicyConfig();
  const deactivateLockdown = useDeactivateLockdown();
  const [showLiftDialog, setShowLiftDialog] = useState(false);
  const isLockdown = config?.lockdown ?? false;
  const policyConfigSupported = POLICY_CONFIG_SUPPORTED;

  const tabs = POLICY_STATS_SUPPORTED
    ? BASE_TABS
    : BASE_TABS.filter((tab) => tab.to !== "/policies/analytics");

  const handleLift = useCallback(() => {
    deactivateLockdown.mutate(undefined, {
      onSuccess: () => setShowLiftDialog(false),
    });
  }, [deactivateLockdown]);

  return (
    <div className="space-y-6">
      {/* Lockdown banner */}
      {policyConfigSupported && isLockdown && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-2xl bg-danger px-5 py-3 text-white shadow-lg">
          <div className="flex items-center gap-3">
            <Lock className="h-5 w-5 animate-pulse" />
            <div>
              <span className="text-sm font-bold uppercase tracking-wide">
                Emergency Lockdown Active
              </span>
              {config?.lockdownReason && (
                <p className="text-xs text-white/80">{config.lockdownReason}</p>
              )}
            </div>
          </div>
          <div className="flex items-center gap-3 text-xs text-white/70">
            {(config?.lockdownBy || config?.lockdownAt) && (
              <span>
                {config.lockdownBy && `by ${config.lockdownBy}`}
                {config.lockdownAt && ` at ${new Date(config.lockdownAt).toLocaleString()}`}
              </span>
            )}
            <button
              type="button"
              onClick={() => setShowLiftDialog(true)}
              className="flex items-center gap-1.5 rounded-full border border-white/40 bg-white/10 px-3 py-1.5 text-xs font-semibold text-white transition hover:bg-white/20"
            >
              <Unlock className="h-3.5 w-3.5" />
              Lift Lockdown
            </button>
          </div>
        </div>
      )}

      {/* Lift lockdown dialog */}
      {policyConfigSupported && showLiftDialog && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
          <Card className="relative z-10 w-full max-w-sm">
            <div className="space-y-4">
              <h3 className="font-display text-lg font-semibold text-ink">
                Lift Emergency Lockdown
              </h3>
              <p className="text-sm text-muted">
                This will restore normal policy evaluation. Jobs will be evaluated against rules again.
              </p>
              <div className="flex justify-end gap-2">
                <Button variant="ghost" size="sm" onClick={() => setShowLiftDialog(false)} disabled={deactivateLockdown.isPending}>
                  Cancel
                </Button>
                <Button variant="primary" size="sm" onClick={handleLift} disabled={deactivateLockdown.isPending}>
                  {deactivateLockdown.isPending ? "Lifting\u2026" : "Lift Lockdown"}
                </Button>
              </div>
            </div>
          </Card>
        </div>
      )}

      {/* Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="font-display text-2xl font-semibold text-ink">
            Policy Studio
          </h2>
          <p className="text-sm text-muted">
            Define safety rules that govern how AI jobs are evaluated.
          </p>
        </div>

        {/* Bundle selector */}
        {bundles.length > 1 && (
          <select
            value={bundleId}
            onChange={(e) => setBundleId(e.target.value)}
            className="rounded-2xl border border-border bg-white/70 px-4 py-2 text-sm text-ink"
          >
            {bundles.map((b) => (
              <option key={b.id} value={b.id}>
                {b.name} (v{b.version})
              </option>
            ))}
          </select>
        )}
      </div>

      {/* Tab nav */}
      <nav className="flex gap-1 overflow-x-auto rounded-full border border-border p-1">
        {tabs.map(({ to, label, icon: Icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            className={({ isActive }) =>
              cn(
                "flex shrink-0 items-center gap-2 rounded-full px-5 py-2 text-xs font-semibold uppercase tracking-widest transition",
                isActive
                  ? "bg-accent/15 text-accent"
                  : "text-muted hover:text-ink",
              )
            }
          >
            <Icon className="h-3.5 w-3.5" />
            {label}
          </NavLink>
        ))}
      </nav>

      {/* Child route content */}
      <Outlet />
    </div>
  );
}

export default function PolicyLayout() {
  return (
    <PolicyBundleProvider>
      <PolicyLayoutInner />
    </PolicyBundleProvider>
  );
}
