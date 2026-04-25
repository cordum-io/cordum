/*
 * DESIGN: "Control Surface" — Packs (Marketplace + Installed)
 * PRD Section 20: Pack management with install/uninstall
 */
import { useState } from "react";
import { motion } from "framer-motion";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import { Input } from "@/components/ui/Input";
import {
  InstrumentCard,
  InstrumentCardBody,
  InstrumentCardFooter,
  InstrumentCardHeader,
} from "@/components/ui/InstrumentCard";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { Tabs } from "@/components/ui/Tabs";
import { Search, Package, Download, Trash2, CheckCircle2, AlertTriangle } from "lucide-react";
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

  const tabs = [
    { id: "installed", label: "Installed", count: packsRes?.items?.length ?? 0 },
    { id: "marketplace", label: "Marketplace", count: marketRes?.items?.length ?? 0 },
  ];
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
      <div className="flex flex-wrap items-center justify-between gap-4">
        <Tabs
          tabs={tabs}
          activeTab={activeTab}
          onChange={setActiveTab}
          variant="segmented"
          ariaLabel="Pack catalog tabs"
          className="w-full sm:w-auto"
        />
        <Input
          type="text"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search packs..."
          icon={<Search className="h-3.5 w-3.5" />}
          className="h-9 w-full text-sm sm:w-64"
        />
      </div>

      {/* Content */}
      {isLoading ? (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {Array.from({ length: 6 }).map((_, i) => <SkeletonCard key={i} />)}
        </div>
      ) : error ? (
        <ErrorBanner
          message={error instanceof Error ? error.message : "Failed to load packs"}
          onRetry={() => activeTab === "installed" ? refetchPacks() : refetchMarket()}
        />
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
    >
      <InstrumentCard className="h-full">
        <InstrumentCardBody className="p-5">
          <InstrumentCardHeader
            title={pack.name}
            icon={<Package className="h-4 w-4 text-cordum" />}
            action={(
              <StatusBadge variant={packStatusVariant(pack.status)} dot>
                {pack.status}
              </StatusBadge>
            )}
          />

          <div className="space-y-3">
            <p className="text-xs font-mono text-muted-foreground">v{pack.version}</p>

            {pack.description && (
              <p className="text-xs text-muted-foreground">{pack.description}</p>
            )}

            {pack.capabilities.length > 0 && (
              <div className="flex flex-wrap gap-1">
                {pack.capabilities.map(c => (
                  <StatusBadge key={c} variant="muted">{c}</StatusBadge>
                ))}
              </div>
            )}
          </div>
        </InstrumentCardBody>

        <InstrumentCardFooter className="flex items-center justify-between">
          <span className="text-xs text-muted-foreground">{pack.author ? `by ${pack.author}` : "\u00A0"}</span>
          <Button variant="danger" size="sm" onClick={onUninstall} loading={isUninstalling}>
            <Trash2 className="w-3 h-3 mr-1" />Uninstall
          </Button>
        </InstrumentCardFooter>
      </InstrumentCard>
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
    >
      <InstrumentCard className="h-full">
        <InstrumentCardBody className="p-5">
          <InstrumentCardHeader
            title={pack.title ?? pack.id}
            icon={<Package className="h-4 w-4 text-cordum" />}
            action={<StatusBadge variant="muted">v{pack.version}</StatusBadge>}
          />

          <div className="space-y-3">
            {pack.description && (
              <p className="text-xs text-muted-foreground">{pack.description}</p>
            )}

            {(pack.capabilities ?? []).length > 0 && (
              <div className="flex flex-wrap gap-1">
                {(pack.capabilities ?? []).map(c => (
                  <StatusBadge key={c} variant="muted">{c}</StatusBadge>
                ))}
              </div>
            )}

            {(pack.riskTags ?? []).length > 0 && (
              <div className="flex flex-wrap gap-1">
                {(pack.riskTags ?? []).map(t => (
                  <StatusBadge key={t} variant="warning">
                    <AlertTriangle className="h-3 w-3" />
                    {t}
                  </StatusBadge>
                ))}
              </div>
            )}
          </div>
        </InstrumentCardBody>

        <InstrumentCardFooter className="flex items-center justify-between">
          <div className="flex flex-col gap-0.5">
            <span className="text-xs text-muted-foreground">{pack.author ? `by ${pack.author}` : "\u00A0"}</span>
            {pack.catalogTitle && (
              <span className="text-xs text-muted-foreground/60">{pack.catalogTitle}</span>
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
        </InstrumentCardFooter>
      </InstrumentCard>
    </motion.div>
  );
}
