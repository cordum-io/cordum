/*
 * SettingsShell — single layout that wraps every /settings/* sub-page.
 * Replaces 12 disconnected settings routes with one coherent shell + a
 * left sub-nav grouped into 5 buckets:
 *   - Plan & Health
 *   - Identity & Access
 *   - Configuration
 *   - Audit Export
 *   - Hub overview (index)
 *
 * Each sub-page keeps its own URL (/settings/<key>) so existing bookmarks,
 * deep links, and tests survive the consolidation. The shell only owns the
 * nav rail + outlet wiring; each child page still renders its own
 * <PageHeader> + content.
 *
 * Locked entries (entitlement-gated) intercept the click and surface
 * UpgradeDialog instead of letting the user land on a half-functional page.
 */
import { useState, type MouseEvent } from "react";
import { NavLink, Outlet } from "react-router-dom";
import { cn } from "@/lib/utils";
import { useLicense } from "@/hooks/useLicense";
import type { LicenseEntitlements } from "@/api/types";
import { UpgradeDialog } from "@/components/license/UpgradeDialog";
import {
  Activity,
  Bell,
  Building2,
  Globe,
  Key,
  Lock,
  Server,
  Settings as SettingsIcon,
  Sparkles,
  Users,
  ShieldCheck,
} from "lucide-react";

interface SettingsSubNavItem {
  path: string;
  label: string;
  icon: typeof SettingsIcon;
  /** Entitlement key(s) required — shows a lock badge when not entitled. */
  entitlement?: (keyof LicenseEntitlements)[];
  /** Match the index route exactly (for the Overview entry). */
  end?: boolean;
}

interface SettingsSubNavGroup {
  label: string;
  items: SettingsSubNavItem[];
}

const SUB_NAV: SettingsSubNavGroup[] = [
  {
    label: "Overview",
    items: [
      { path: "/settings", label: "Hub", icon: SettingsIcon, end: true },
    ],
  },
  {
    label: "Plan & Health",
    items: [
      { path: "/settings/license", label: "License & Limits", icon: Sparkles },
      { path: "/settings/health", label: "System Health", icon: Activity },
    ],
  },
  {
    label: "Identity & Access",
    items: [
      { path: "/settings/users", label: "Users & RBAC", icon: Users },
      { path: "/settings/keys", label: "API Keys", icon: Key },
      { path: "/settings/sso", label: "SSO & SAML", icon: Building2, entitlement: ["sso"] },
      { path: "/settings/scim", label: "SCIM", icon: Key, entitlement: ["scim"] },
    ],
  },
  {
    label: "Configuration",
    items: [
      { path: "/settings/config", label: "System Config", icon: SettingsIcon },
      { path: "/settings/environments", label: "Environments", icon: Globe },
      { path: "/settings/notifications", label: "Notifications", icon: Bell },
      { path: "/settings/mcp", label: "MCP Server", icon: Server },
    ],
  },
  {
    label: "Audit Export",
    items: [
      {
        path: "/settings/audit-export",
        label: "SIEM Export",
        icon: ShieldCheck,
        entitlement: ["siemExport", "auditExport", "legalHold"],
      },
    ],
  },
];

function isEntitled(
  entitlements: LicenseEntitlements | undefined,
  keys?: (keyof LicenseEntitlements)[],
): boolean {
  if (!keys || keys.length === 0) return true;
  if (!entitlements) return false;
  return keys.some((k) => entitlements[k] === true);
}

export default function SettingsShell() {
  const { data: license, isLoading } = useLicense();
  const entitlements = license?.entitlements;
  const [lockedFeature, setLockedFeature] = useState<string | null>(null);

  return (
    <div className="grid grid-cols-1 gap-6 lg:grid-cols-[14rem_1fr]">
      <aside aria-label="Settings navigation" className="space-y-6">
        {SUB_NAV.map((group) => (
          <div key={group.label}>
            <h3 className="px-3 mb-2 text-xs font-semibold uppercase tracking-[0.1em] text-muted-foreground/60">
              {group.label}
            </h3>
            <nav className="space-y-0.5">
              {group.items.map((item) => {
                const entitled = isEntitled(entitlements, item.entitlement);
                // Fail open while the license is still loading so a slow
                // network doesn't briefly block users from features they
                // actually have. Mirrors components/EntitlementGate.tsx.
                const locked = !!item.entitlement && !isLoading && !entitled;

                const handleClick = (event: MouseEvent<HTMLAnchorElement>) => {
                  if (locked) {
                    event.preventDefault();
                    setLockedFeature(item.label);
                  }
                };

                return (
                  <NavLink
                    key={item.path}
                    to={item.path}
                    end={item.end}
                    aria-disabled={locked || undefined}
                    onClick={handleClick}
                    onKeyDown={(event) => {
                      // NavLink renders an <a> — Enter triggers click on
                      // anchors automatically, so onClick covers keyboard
                      // activation. Space does not, so handle it here.
                      if (locked && event.key === " ") {
                        event.preventDefault();
                        setLockedFeature(item.label);
                      }
                    }}
                    className={({ isActive }) =>
                      cn(
                        "flex items-center gap-2 px-3 py-2 rounded-xl text-sm transition-colors",
                        isActive
                          ? "bg-cordum/10 text-cordum font-medium"
                          : "text-muted-foreground hover:text-foreground hover:bg-surface-2",
                      )
                    }
                  >
                    <item.icon className="w-4 h-4 shrink-0" />
                    <span className="flex-1 truncate">{item.label}</span>
                    {locked && (
                      <Lock
                        className="w-3 h-3 text-muted-foreground/60 shrink-0"
                        aria-label="Enterprise feature"
                      />
                    )}
                  </NavLink>
                );
              })}
            </nav>
          </div>
        ))}
      </aside>
      <main className="min-w-0">
        <Outlet />
      </main>
      <UpgradeDialog
        open={lockedFeature !== null}
        onClose={() => setLockedFeature(null)}
        feature={lockedFeature ?? ""}
        currentPlan={license?.plan}
      />
    </div>
  );
}

export const SETTINGS_SUB_NAV = SUB_NAV;
