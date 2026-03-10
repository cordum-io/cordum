/*
 * DESIGN: "Control Surface" — Environments
 * PRD Section 28: Environment cards with connection details
 */
import { useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { get } from "@/api/client";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { EmptyState } from "@/components/ui/EmptyState";
import { Globe, Copy, Plus, Server, Shield, ExternalLink } from "lucide-react";
import { cn } from "@/lib/utils";
import { toast } from "sonner";

interface Environment {
  id: string;
  name: string;
  url: string;
  status: "active" | "inactive";
  region: string;
  version: string;
  workers: number;
}

export default function SettingsEnvironmentsPage() {
  const { data: envs, isLoading } = useQuery({
    queryKey: ["environments"],
    queryFn: async () => {
      const res = await get<{ data?: Environment[] }>("/environments");
      return res.data || [];
    },
  });

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader title="Environments" subtitle="Manage deployment environments and connections" actions={<><Button variant="primary" size="sm" onClick={() => toast.info("Not configured")}>
          <Plus className="w-3 h-3 mr-1" />Add Environment
        </Button></>} />

      {isLoading ? (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">{Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />)}</div>
      ) : !envs?.length ? (
        <EmptyState icon={<Globe className="w-8 h-8" />} title="No environments" description="Add an environment to connect to a Cordum cluster" />
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {envs.map((env, i) => (
            <motion.div key={env.id} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: i * 0.05 }}
              className={cn("instrument-card", env.status === "active" && "status-healthy")}>
              <div className="flex items-start justify-between mb-3">
                <div className="flex items-center gap-2">
                  <Server className="w-4 h-4 text-cordum" />
                  <span className="text-sm font-display font-semibold text-foreground">{env.name}</span>
                </div>
                <StatusBadge variant={env.status === "active" ? "healthy" : "muted"} dot>{env.status}</StatusBadge>
              </div>
              <div className="space-y-2 mb-4">
                <div className="flex items-center justify-between">
                  <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">URL</span>
                  <div className="flex items-center gap-1">
                    <span className="text-xs font-mono text-foreground">{env.url}</span>
                    <button onClick={() => { navigator.clipboard.writeText(env.url); toast.success("Copied"); }} className="p-0.5 rounded hover:bg-surface-2">
                      <Copy className="w-3 h-3 text-muted-foreground" />
                    </button>
                  </div>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Region</span>
                  <span className="text-xs text-foreground">{env.region}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Version</span>
                  <span className="text-xs font-mono text-foreground">{env.version}</span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Workers</span>
                  <span className="text-xs text-foreground">{env.workers} connected</span>
                </div>
              </div>
              <div className="flex gap-2 pt-3 border-t border-border">
                <Button variant="outline" size="sm" className="flex-1" onClick={() => toast.info("Not configured")}>
                  <Shield className="w-3 h-3 mr-1" />Configure
                </Button>
                <Button variant="ghost" size="sm" onClick={() => window.open(env.url, "_blank", "noopener,noreferrer")}>
                  <ExternalLink className="w-3 h-3" />
                </Button>
              </div>
            </motion.div>
          ))}
        </div>
      )}
    </motion.div>
  );
}
