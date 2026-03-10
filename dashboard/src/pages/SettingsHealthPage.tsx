/*
 * DESIGN: "Control Surface" — System Health
 * PRD Section 29: Service health table + system info
 */
import { motion } from "framer-motion";
import { PageHeader } from "@/components/layout/PageHeader";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { SkeletonCard, SkeletonTable } from "@/components/ui/Skeleton";
import { Activity, Server, Database, Wifi, Clock, GitCommit, Hash } from "lucide-react";
import { useStatus } from "@/hooks/useStatus";
import type { GatewayStatus } from "@/hooks/useStatus";

interface ServiceHealth {
  name: string;
  status: "healthy" | "degraded" | "down";
  latency: string;
  uptime: string;
  lastCheck: string;
}

function formatUptime(seconds?: number): string {
  if (typeof seconds !== "number" || !Number.isFinite(seconds) || seconds < 0) return "—";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  if (days > 0) return `${days}d ${hours}h`;
  const minutes = Math.floor((seconds % 3600) / 60);
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}

function deriveServices(status?: GatewayStatus): ServiceHealth[] {
  if (!status) return [];

  const lastCheck = status.time
    ? new Date(status.time).toLocaleTimeString()
    : "—";

  const uptimeStr = formatUptime(status.uptime_seconds);

  return [
    {
      name: "API Gateway",
      status: "healthy",
      latency: "—",
      uptime: uptimeStr,
      lastCheck,
    },
    {
      name: "NATS Bus",
      status: status.nats?.connected ? "healthy" : "down",
      latency: "—",
      uptime: "—",
      lastCheck,
    },
    {
      name: "Redis Store",
      status: status.redis?.ok ? "healthy" : "down",
      latency: "—",
      uptime: "—",
      lastCheck,
    },
    {
      name: "Worker Pool",
      status:
        (status.workers?.count ?? 0) > 0 ? "healthy" : "degraded",
      latency: "—",
      uptime: "—",
      lastCheck,
    },
  ];
}

export default function SettingsHealthPage() {
  const { data: status, isLoading } = useStatus();

  const services = deriveServices(status);
  const serviceIcon = (name: string) => {
    if (name.includes("API")) return Server;
    if (name.includes("NATS")) return Wifi;
    if (name.includes("Redis")) return Database;
    if (name.includes("Worker")) return Activity;
    return Activity;
  };

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader title="System Health" subtitle="Monitor service status and system information" />

      {/* System Info */}
      {isLoading ? (
        <div className="grid grid-cols-3 gap-4">{Array.from({ length: 3 }).map((_, i) => <SkeletonCard key={i} />)}</div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          {[
            { label: "Version", value: status?.build?.version || "—", icon: GitCommit },
            { label: "Uptime", value: formatUptime(status?.uptime_seconds), icon: Clock },
            { label: "Instance", value: status?.instance_id ? status.instance_id.slice(0, 12) : "—", icon: Hash },
          ].map((item, i) => {
            const Icon = item.icon;
            return (
              <motion.div key={item.label} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: i * 0.05 }}
                className="instrument-card">
                <div className="flex items-center gap-2 mb-2">
                  <Icon className="w-4 h-4 text-cordum" />
                  <span className="text-xs font-mono text-muted-foreground uppercase tracking-wider">{item.label}</span>
                </div>
                <span className="text-lg font-mono font-bold text-foreground">{item.value}</span>
              </motion.div>
            );
          })}
        </div>
      )}

      {/* Services Table */}
      {isLoading ? <SkeletonTable rows={4} /> : (
        <div className="instrument-card overflow-hidden">
          <div className="px-4 py-3 border-b border-border bg-surface-0">
            <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Service Status</p>
          </div>
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-surface-0">
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Service</th>
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Status</th>
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Latency</th>
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Uptime</th>
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Last Check</th>
              </tr>
            </thead>
            <tbody>
              {services.map((svc, i) => {
                const Icon = serviceIcon(svc.name);
                return (
                  <motion.tr key={svc.name} initial={{ opacity: 0 }} animate={{ opacity: 1 }} transition={{ delay: i * 0.03 }}
                    className="border-b border-border last:border-0 hover:bg-surface-1 transition-colors">
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-2">
                        <Icon className="w-3.5 h-3.5 text-muted-foreground" />
                        <span className="font-medium text-foreground">{svc.name}</span>
                      </div>
                    </td>
                    <td className="px-4 py-3"><StatusBadge variant={svc.status === "healthy" ? "healthy" : svc.status === "degraded" ? "warning" : "danger"} dot>{svc.status}</StatusBadge></td>
                    <td className="px-4 py-3 font-mono text-xs text-muted-foreground">{svc.latency}</td>
                    <td className="px-4 py-3 text-xs text-muted-foreground">{svc.uptime}</td>
                    <td className="px-4 py-3 text-xs text-muted-foreground">{svc.lastCheck}</td>
                  </motion.tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </motion.div>
  );
}
