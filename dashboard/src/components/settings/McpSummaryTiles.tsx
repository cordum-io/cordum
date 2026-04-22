import type { ReactNode } from "react";
import { Server, Shield, Wrench, Zap } from "lucide-react";
import type { BadgeVariant } from "@/components/ui/StatusBadge";
import { StatTile } from "@/components/ui/StatTile";

interface McpSummaryTilesProps {
  isRunning: boolean;
  toolCount: number;
  enabledToolCount: number;
  connectedClients: number;
  uptimeLabel: string;
}

interface SummaryItem {
  label: string;
  value: string | number;
  helperText: string;
  icon: ReactNode;
  accent: BadgeVariant;
}

export function McpSummaryTiles({
  isRunning,
  toolCount,
  enabledToolCount,
  connectedClients,
  uptimeLabel,
}: McpSummaryTilesProps) {
  const defaultAccent: BadgeVariant = "cordum";
  const items: SummaryItem[] = [
    {
      label: "Servers",
      value: 1,
      helperText: isRunning ? "1 connected" : "0 connected",
      icon: <Server className="h-4 w-4" />,
      accent: isRunning ? "healthy" : "muted",
    },
    {
      label: "Tools",
      value: toolCount,
      helperText: `${enabledToolCount} enabled`,
      icon: <Wrench className="h-4 w-4" />,
      accent: defaultAccent,
    },
    {
      label: "Clients",
      value: connectedClients,
      helperText: "Connected clients",
      icon: <Zap className="h-4 w-4" />,
      accent: connectedClients > 0 ? "info" : "muted",
    },
    {
      label: "Uptime",
      value: uptimeLabel,
      helperText: "Current runtime",
      icon: <Shield className="h-4 w-4" />,
      accent: defaultAccent,
    },
  ];

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
      {items.map((item) => (
        <StatTile
          key={item.label}
          accent={item.accent}
          label={item.label}
          value={item.value}
          icon={item.icon}
          helperText={item.helperText}
        />
      ))}
    </div>
  );
}
