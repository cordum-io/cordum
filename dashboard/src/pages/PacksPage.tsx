/*
 * DESIGN: "Control Surface" — Packs (Marketplace + Installed)
 * PRD Section 20: Pack management with install/uninstall
 */
import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { get, post } from "@/api/client";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { Search, Package, Download, Trash2, ExternalLink, RefreshCw, Star, CheckCircle2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { toast } from "sonner";

interface Pack {
  name: string;
  version: string;
  description: string;
  author: string;
  installed: boolean;
  stars?: number;
  topics?: string[];
}

export default function PacksPage() {
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState("installed");
  const [search, setSearch] = useState("");

  const { data: packs, isLoading, error } = useQuery({
    queryKey: ["packs"],
    queryFn: async () => {
      const res = await get<{ data?: Pack[] }>("/packs");
      return res.data ?? [];
    },
  });

  const installMutation = useMutation({
    mutationFn: async (name: string) => post(`/packs/${name}/install`, {}),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["packs"] }); toast.success("Pack installed"); },
  });

  const uninstallMutation = useMutation({
    mutationFn: async (name: string) => post(`/packs/${name}/uninstall`, {}),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["packs"] }); toast.success("Pack uninstalled"); },
  });

  const tabs = ["installed", "marketplace"];
  const filtered = (packs || []).filter(p => {
    const matchTab = activeTab === "installed" ? p.installed : !p.installed;
    const matchSearch = !search || p.name.toLowerCase().includes(search.toLowerCase()) || p.description.toLowerCase().includes(search.toLowerCase());
    return matchTab && matchSearch;
  });

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader title="Packs" subtitle="Extend Cordum with community and custom packs" />

      {/* Tabs + Search */}
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-1 p-1 rounded-lg bg-surface-1">
          {tabs.map(tab => (
            <button
              key={tab}
              onClick={() => setActiveTab(tab)}
              className={cn(
                "px-4 py-1.5 text-xs font-medium rounded-md transition-colors capitalize",
                activeTab === tab ? "bg-cordum/10 text-cordum" : "text-muted-foreground hover:text-foreground",
              )}
            >
              {tab}
            </button>
          ))}
        </div>
        <div className="relative w-64">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search packs..."
            className="h-8 w-full pl-9 pr-3 text-xs bg-surface-1 border border-border rounded-md text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
          />
        </div>
      </div>

      {/* Content */}
      {isLoading ? (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {Array.from({ length: 6 }).map((_, i) => <SkeletonCard key={i} />)}
        </div>
      ) : error ? (
        <div className="instrument-card p-8 text-center">
          <p className="text-sm text-red-400">Failed to load packs</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => queryClient.invalidateQueries({ queryKey: ["packs"] })}>
            <RefreshCw className="w-3 h-3 mr-1" />Retry
          </Button>
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState icon={<Package className="w-8 h-8" />}
          title={activeTab === "installed" ? "No packs installed" : "No packs found"}
          description={activeTab === "installed" ? "Browse the marketplace to install packs" : "Try a different search term"}
        />
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {filtered.map((pack, i) => (
            <motion.div
              key={pack.name}
              initial={{ opacity: 0, y: 8 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: i * 0.04 }}
              className="instrument-card p-5 flex flex-col"
            >
              <div className="flex items-start justify-between mb-3">
                <div className="flex items-center gap-2">
                  <Package className="w-4 h-4 text-cordum" />
                  <span className="text-sm font-display font-semibold text-foreground">{pack.name}</span>
                </div>
                <StatusBadge variant={pack.installed ? "healthy" : "muted"}>
                  {pack.installed ? "Installed" : `v${pack.version}`}
                </StatusBadge>
              </div>
              <p className="text-xs text-muted-foreground flex-1 mb-3">{pack.description}</p>
              {pack.topics && (
                <div className="flex flex-wrap gap-1 mb-3">
                  {pack.topics.map(t => (
                    <span key={t} className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-surface-2 text-muted-foreground">{t}</span>
                  ))}
                </div>
              )}
              <div className="flex items-center justify-between pt-3 border-t border-border">
                <span className="text-[10px] text-muted-foreground">by {pack.author}</span>
                {pack.installed ? (
                  <Button variant="danger" size="sm" onClick={() => uninstallMutation.mutate(pack.name)} loading={uninstallMutation.isPending}>
                    <Trash2 className="w-3 h-3 mr-1" />Uninstall
                  </Button>
                ) : (
                  <Button variant="primary" size="sm" onClick={() => installMutation.mutate(pack.name)} loading={installMutation.isPending}>
                    <Download className="w-3 h-3 mr-1" />Install
                  </Button>
                )}
              </div>
            </motion.div>
          ))}
        </div>
      )}
    </motion.div>
  );
}
