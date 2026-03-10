/*
 * DESIGN: "Control Surface" — Packs (Marketplace + Installed)
 * PRD Section 20: Pack management with install/uninstall
 */
import { useState } from "react";
import { motion } from "framer-motion";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { Search, Package, Download, Trash2, RefreshCw, CheckCircle2, AlertTriangle } from "lucide-react";
import { cn } from "@/lib/utils";
import type { Pack, MarketplacePack } from "@/api/types";
import { usePacks, useMarketplacePacks, useInstallPack, useUninstallPack } from "@/hooks/usePacks";

function packStatusVariant(status: string): BadgeVariant {
  switch (status) {
    case "ACTIVE": case "active": return "healthy";
    case "INACTIVE": case "inactive": return "muted";
    case "DISABLED": case "disabled": return "danger";
    default: return "muted";
  }
}

export default function PacksPage() {
  const [activeTab, setActiveTab] = useState("installed");
  const [search, setSearch] = useState("");

  const { data: packsRes, isLoading: packsLoading, error: packsError, refetch: refetchPacks } = usePacks();
  const { data: marketRes, isLoading: marketLoading, error: marketError, refetch: refetchMarket } = useMarketplacePacks();
  const installMutation = useInstallPack();
  const uninstallMutation = useUninstallPack();

  const tabs = ["installed", "marketplace"];
  const q = search.toLowerCase();

  const installedPacks = (packsRes?.items ?? []).filter(p =>
    !search || p.name.toLowerCase().includes(q) || (p.description ?? "").toLowerCase().includes(q),
  );

  const marketplacePacks = (marketRes?.items ?? []).filter(p =>
    !search || (p.title ?? "").toLowerCase().includes(q) || (p.description ?? "").toLowerCase().includes(q),
  );

  const isLoading = activeTab === "installed" ? packsLoading : marketLoading;
  const error = activeTab === "installed" ? packsError : marketError;

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader title="Packs" subtitle="Extend Cordum with community and custom packs" />

      {/* Tabs + Search */}
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-1 p-1 rounded-2xl bg-surface-1">
          {tabs.map(tab => (
            <button
              key={tab}
              onClick={() => setActiveTab(tab)}
              className={cn(
                "px-4 py-1.5 text-xs font-medium rounded-2xl transition-colors capitalize",
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
            className="h-8 w-full pl-9 pr-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
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
          <p className="text-sm text-destructive">Failed to load packs</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => activeTab === "installed" ? refetchPacks() : refetchMarket()}>
            <RefreshCw className="w-3 h-3 mr-1" />Retry
          </Button>
        </div>
      ) : activeTab === "installed" ? (
        installedPacks.length === 0 ? (
          <EmptyState
            icon={<Package className="w-8 h-8" />}
            title="No packs installed"
            description="Browse the marketplace to install packs"
            action={
              <Button variant="outline" size="sm" onClick={() => setActiveTab("marketplace")}>
                Browse Marketplace
              </Button>
            }
          />
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {installedPacks.map((pack, i) => (
              <InstalledPackCard
                key={pack.id}
                pack={pack}
                index={i}
                isUninstalling={uninstallMutation.isPending && uninstallMutation.variables === pack.id}
                onUninstall={() => uninstallMutation.mutate(pack.id)}
              />
            ))}
          </div>
        )
      ) : (
        marketplacePacks.length === 0 ? (
          <EmptyState
            icon={<Package className="w-8 h-8" />}
            title="No packs found"
            description="Try a different search term"
          />
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {marketplacePacks.map((pack, i) => (
              <MarketplacePackCard
                key={`${pack.catalogId}-${pack.id}`}
                pack={pack}
                index={i}
                isInstalling={installMutation.isPending && installMutation.variables?.packId === pack.id}
                onInstall={() => installMutation.mutate({
                  catalogId: pack.catalogId ?? "",
                  packId: pack.id,
                  version: pack.version,
                  url: pack.url,
                  sha256: pack.sha256,
                })}
              />
            ))}
          </div>
        )
      )}
    </motion.div>
  );
}

/* ------------------------------------------------------------------ */
/*  Installed pack card                                                */
/* ------------------------------------------------------------------ */

function InstalledPackCard({ pack, index, isUninstalling, onUninstall }: {
  pack: Pack;
  index: number;
  isUninstalling: boolean;
  onUninstall: () => void;
}) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ delay: index * 0.04 }}
      className="instrument-card flex flex-col"
    >
      <div className="flex items-start justify-between mb-3">
        <div className="flex items-center gap-2">
          <Package className="w-4 h-4 text-cordum" />
          <span className="text-sm font-display font-semibold text-foreground">{pack.name}</span>
        </div>
        <StatusBadge variant={packStatusVariant(pack.status)} dot>
          {pack.status}
        </StatusBadge>
      </div>

      <span className="text-[10px] font-mono text-muted-foreground mb-2">v{pack.version}</span>

      {pack.description && (
        <p className="text-xs text-muted-foreground flex-1 mb-3">{pack.description}</p>
      )}

      {pack.capabilities.length > 0 && (
        <div className="flex flex-wrap gap-1 mb-3">
          {pack.capabilities.map(c => (
            <span key={c} className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-surface-2 text-muted-foreground">{c}</span>
          ))}
        </div>
      )}

      <div className="flex items-center justify-between pt-3 border-t border-border">
        <span className="text-[10px] text-muted-foreground">{pack.author ? `by ${pack.author}` : "\u00A0"}</span>
        <Button variant="danger" size="sm" onClick={onUninstall} loading={isUninstalling}>
          <Trash2 className="w-3 h-3 mr-1" />Uninstall
        </Button>
      </div>
    </motion.div>
  );
}

/* ------------------------------------------------------------------ */
/*  Marketplace pack card                                              */
/* ------------------------------------------------------------------ */

function MarketplacePackCard({ pack, index, isInstalling, onInstall }: {
  pack: MarketplacePack;
  index: number;
  isInstalling: boolean;
  onInstall: () => void;
}) {
  const alreadyInstalled = !!pack.installedVersion;

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ delay: index * 0.04 }}
      className="instrument-card flex flex-col"
    >
      <div className="flex items-start justify-between mb-3">
        <div className="flex items-center gap-2">
          <Package className="w-4 h-4 text-cordum" />
          <span className="text-sm font-display font-semibold text-foreground">{pack.title ?? pack.id}</span>
        </div>
        <StatusBadge variant="muted">v{pack.version}</StatusBadge>
      </div>

      {pack.description && (
        <p className="text-xs text-muted-foreground flex-1 mb-3">{pack.description}</p>
      )}

      {(pack.capabilities ?? []).length > 0 && (
        <div className="flex flex-wrap gap-1 mb-2">
          {(pack.capabilities ?? []).map(c => (
            <span key={c} className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-surface-2 text-muted-foreground">{c}</span>
          ))}
        </div>
      )}

      {(pack.riskTags ?? []).length > 0 && (
        <div className="flex flex-wrap gap-1 mb-3">
          {(pack.riskTags ?? []).map(t => (
            <span key={t} className="inline-flex items-center gap-1 text-[10px] font-mono px-1.5 py-0.5 rounded bg-[var(--color-warning)]/10 text-[var(--color-warning)]">
              <AlertTriangle className="w-2.5 h-2.5" />{t}
            </span>
          ))}
        </div>
      )}

      <div className="flex items-center justify-between pt-3 border-t border-border">
        <div className="flex flex-col gap-0.5">
          <span className="text-[10px] text-muted-foreground">{pack.author ? `by ${pack.author}` : "\u00A0"}</span>
          {pack.catalogTitle && (
            <span className="text-[10px] text-muted-foreground/60">{pack.catalogTitle}</span>
          )}
        </div>
        {alreadyInstalled ? (
          <StatusBadge variant="healthy" dot>
            <CheckCircle2 className="w-3 h-3 mr-0.5" />Installed
          </StatusBadge>
        ) : (
          <Button variant="primary" size="sm" onClick={onInstall} loading={isInstalling}>
            <Download className="w-3 h-3 mr-1" />Install
          </Button>
        )}
      </div>
    </motion.div>
  );
}
