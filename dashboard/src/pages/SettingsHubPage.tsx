import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { motion } from "framer-motion";
import {
  Activity,
  Bell,
  Building2,
  Globe,
  Key,
  Lock,
  Server,
  Settings,
  Sparkles,
  Users,
} from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";
import { useLicense } from "@/hooks/useLicense";
import { UpgradeDialog } from "@/components/license/UpgradeDialog";
import type { LicenseEntitlements } from "@/api/types";
import { cn } from "@/lib/utils";

interface SettingsCard {
  icon: typeof Settings;
  title: string;
  description: string;
  path: string;
  /** Entitlement key(s) required — shows lock badge when not entitled */
  entitlement?: (keyof LicenseEntitlements)[];
}

const settingsCards: SettingsCard[] = [
  { icon: Settings, title: "System Config", description: "Core system configuration and feature flags", path: "/settings/config" },
  { icon: Globe, title: "Environments", description: "Read-only deployment environment inventory from saved config", path: "/settings/environments" },
  { icon: Activity, title: "System Health", description: "Monitor system health and diagnostics", path: "/settings/health" },
  { icon: Key, title: "API Keys", description: "Manage API keys and access tokens", path: "/settings/keys" },
  { icon: Server, title: "MCP Server", description: "Configure MCP server connections", path: "/settings/mcp" },
  { icon: Bell, title: "Notifications", description: "Config-backed delivery channels and routing rules", path: "/settings/notifications" },
  { icon: Users, title: "Users & RBAC", description: "User management and role assignments", path: "/settings/users" },
  { icon: Building2, title: "SSO & SAML", description: "Enterprise identity provider configuration and operator handoff details", path: "/settings/sso", entitlement: ["sso"] },
  { icon: Key, title: "SCIM Provisioning", description: "Publish the SCIM endpoint, rotate provisioning tokens, and inspect synced users", path: "/settings/scim", entitlement: ["scim"] },
  { icon: Activity, title: "Audit Export", description: "SIEM audit event export — webhook, syslog, Datadog, CloudWatch", path: "/settings/audit-export", entitlement: ["siemExport", "auditExport", "legalHold"] },
  { icon: Sparkles, title: "License & Limits", description: "Current plan, entitlements, telemetry mode, and capacity limits", path: "/settings/license" },
];

function isEntitled(entitlements: LicenseEntitlements | undefined, keys?: (keyof LicenseEntitlements)[]): boolean {
  if (!keys || keys.length === 0) return true;
  if (!entitlements) return false;
  return keys.some((k) => entitlements[k] === true);
}

export default function SettingsHubPage() {
  const navigate = useNavigate();
  const { data: license, isLoading } = useLicense();
  const entitlements = license?.entitlements;
  const [lockedFeature, setLockedFeature] = useState<string | null>(null);

  return (
    <div className="space-y-6">
      <PageHeader
        label="Settings"
        title="Settings"
        subtitle="Configure your Cordum instance."
      />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
        {settingsCards.map((card, i) => {
          const entitled = isEntitled(entitlements, card.entitlement);
          // Fail open while the license is loading so a slow network doesn't
          // block users from features they actually have. Mirrors
          // components/EntitlementGate.tsx and pages/SettingsShell.tsx.
          const locked = !!card.entitlement && !isLoading && !entitled;

          const handleClick = () => {
            if (locked) {
              setLockedFeature(card.title);
              return;
            }
            navigate(card.path);
          };

          return (
            <motion.button
              key={card.path}
              initial={{ opacity: 0, y: 12 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: i * 0.04, duration: 0.3 }}
              onClick={handleClick}
              data-locked={locked ? "true" : "false"}
              className="instrument-card text-left transition-all duration-200 group hover:bg-surface-2/50"
            >
              <div className="flex items-start gap-4">
                <div className={cn(
                  "flex h-10 w-10 shrink-0 items-center justify-center rounded-2xl transition-colors",
                  locked ? "bg-muted/20 group-hover:bg-muted/30" : "bg-cordum/10 group-hover:bg-cordum/20",
                )}>
                  <card.icon className={cn("h-5 w-5", locked ? "text-muted-foreground" : "text-cordum")} />
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <h2 className={cn(
                      "text-sm font-display font-semibold transition-colors",
                      locked ? "text-muted-foreground" : "text-foreground group-hover:text-cordum",
                    )}>
                      {card.title}
                    </h2>
                    {locked && (
                      <span className="inline-flex items-center gap-0.5 rounded-full border border-border/60 bg-surface-1 px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                        <Lock className="h-2.5 w-2.5" />
                        Enterprise
                      </span>
                    )}
                  </div>
                  <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                    {card.description}
                  </p>
                </div>
              </div>
            </motion.button>
          );
        })}
      </div>
      <UpgradeDialog
        open={lockedFeature !== null}
        onClose={() => setLockedFeature(null)}
        feature={lockedFeature ?? ""}
        currentPlan={license?.plan}
      />
    </div>
  );
}
