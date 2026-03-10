import { useNavigate } from "react-router-dom";
import { motion } from "framer-motion";
import {
  Settings, Globe, Activity, Key, Server, Bell, Users, ShieldCheck, ShieldAlert,
} from "lucide-react";
import { PageHeader } from "@/components/layout/PageHeader";

const settingsCards = [
  { icon: Settings, title: "System Config", description: "Core system configuration and feature flags", path: "/settings/config" },
  { icon: Globe, title: "Environments", description: "Manage deployment environments", path: "/settings/environments" },
  { icon: Activity, title: "System Health", description: "Monitor system health and diagnostics", path: "/settings/health" },
  { icon: Key, title: "API Keys", description: "Manage API keys and access tokens", path: "/settings/keys" },
  { icon: Server, title: "MCP Server", description: "Configure MCP server connections", path: "/settings/mcp" },
  { icon: Bell, title: "Notifications", description: "Notification channels and preferences", path: "/settings/notifications" },
  { icon: Users, title: "Users & RBAC", description: "User management and role assignments", path: "/settings/users" },
  { icon: ShieldCheck, title: "Input Safety", description: "Configure input safety policies", path: "/govern/input-rules" },
  { icon: ShieldAlert, title: "Output Safety", description: "Configure output quarantine settings", path: "/govern/output-rules" },
];

export default function SettingsHubPage() {
  const navigate = useNavigate();

  return (
    <div className="space-y-6">
      <PageHeader
        label="Settings"
        title="Settings"
        subtitle="Configure your Cordum instance."
      />

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {settingsCards.map((card, i) => (
          <motion.button
            key={card.path}
            initial={{ opacity: 0, y: 12 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ delay: i * 0.04, duration: 0.3 }}
            onClick={() => navigate(card.path)}
            className="instrument-card text-left hover:bg-surface-2/50 transition-all duration-200 group"
          >
            <div className="flex items-start gap-4">
              <div className="w-10 h-10 rounded-2xl bg-cordum/10 flex items-center justify-center shrink-0 group-hover:bg-cordum/20 transition-colors">
                <card.icon className="w-5 h-5 text-cordum" />
              </div>
              <div className="min-w-0">
                <h3 className="text-sm font-display font-semibold text-foreground group-hover:text-cordum transition-colors">
                  {card.title}
                </h3>
                <p className="text-xs text-muted-foreground mt-1 leading-relaxed">
                  {card.description}
                </p>
              </div>
            </div>
          </motion.button>
        ))}
      </div>
    </div>
  );
}
