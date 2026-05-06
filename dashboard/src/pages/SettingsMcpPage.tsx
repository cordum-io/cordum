/*
 * DESIGN: central design-system pilot for the MCP settings surface.
 * This page should compose shared primitives only: PageHeader, StatTile,
 * Tabs, InstrumentCard, CollapsibleSection, EmptyState, ErrorBanner, etc.
 */
import { motion } from "framer-motion";
import { AlertTriangle } from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/layout/PageHeader";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { InstrumentCard, InstrumentCardBody } from "@/components/ui/InstrumentCard";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { McpServerPanel } from "@/components/settings/McpServerPanel";
import { McpSummaryTiles } from "@/components/settings/McpSummaryTiles";
import {
  useMcpStatus,
  useMcpConfig,
  useSetMcpConfig,
  useMcpTools,
  useMcpResources,
} from "@/hooks/useSettings";

function formatUptime(seconds?: number): string {
  if (!seconds) return "—";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3_600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
  const hours = Math.floor(seconds / 3_600);
  const minutes = Math.floor((seconds % 3_600) / 60);
  return `${hours}h ${minutes}m`;
}

export default function SettingsMcpPage() {
  const { data: mcpConfig, isLoading: configLoading } = useMcpConfig();
  const { data: mcpStatus } = useMcpStatus();
  const { data: tools = [], isLoading: toolsLoading } = useMcpTools();
  const { data: resources = [], isLoading: resourcesLoading } = useMcpResources();
  const setMcpConfig = useSetMcpConfig();

  const isLoading = configLoading || toolsLoading || resourcesLoading;
  const uptimeLabel = formatUptime(mcpStatus?.uptime);
  const toolCount = tools.length;
  const enabledToolCount = tools.filter((tool) => tool.enabled).length;
  const resourceCount = resources.length;
  const serverUrl = mcpConfig
    ? `${mcpConfig.transport}://${window.location.hostname}:${mcpConfig.port}`
    : "";

  const handleCopyUrl = async () => {
    if (!serverUrl) {
      toast.error("MCP URL is not available yet");
      return;
    }
    try {
      await navigator.clipboard.writeText(serverUrl);
      toast.success("MCP URL copied");
    } catch {
      toast.error("Failed to copy MCP URL");
    }
  };

  const handleToggleRuntime = () => {
    if (!mcpConfig) return;
    const nextEnabled = !mcpConfig.enabled;
    setMcpConfig.mutate(
      { enabled: nextEnabled },
      {
        onSuccess: () => {
          toast.success(nextEnabled ? "MCP runtime enabled" : "MCP runtime disabled");
        },
      },
    );
  };

  return (
    <motion.div
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      className="space-y-6"
      data-testid="settings-mcp-page"
    >
      <PageHeader
        title="MCP Server"
        subtitle={`1 server · ${toolCount} tools · ${resourceCount} resources`}
      />

      {isLoading ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4" data-testid="settings-mcp-loading">
          <SkeletonCard />
          <SkeletonCard />
          <SkeletonCard />
          <SkeletonCard />
        </div>
      ) : !mcpConfig ? (
        <ErrorBanner
          title="Failed to load MCP configuration"
          message="Configuration data is unavailable."
          className="rounded-3xl border border-border bg-card"
        />
      ) : (
        <>
          {!mcpConfig.enabled && (
            <InstrumentCard accent="warning">
              <InstrumentCardBody className="flex items-start gap-3">
                <div className="rounded-2xl bg-warning/15 p-2 text-warning">
                  <AlertTriangle className="h-4 w-4" />
                </div>
                <div>
                  <p className="text-sm font-medium text-foreground">MCP server is disabled</p>
                  <p className="mt-1 text-xs text-muted-foreground">
                    Enable it in configuration to allow MCP connections and expose tools/resources.
                  </p>
                </div>
              </InstrumentCardBody>
            </InstrumentCard>
          )}

          <McpSummaryTiles
            isRunning={Boolean(mcpStatus?.running)}
            toolCount={toolCount}
            enabledToolCount={enabledToolCount}
            connectedClients={mcpStatus?.connectedClients ?? 0}
            uptimeLabel={uptimeLabel}
          />
          <McpServerPanel
            config={mcpConfig}
            status={mcpStatus}
            tools={tools}
            resources={resources}
            serverUrl={serverUrl}
            uptimeLabel={uptimeLabel}
            onCopyUrl={handleCopyUrl}
            onToggleRuntime={handleToggleRuntime}
            runtimeTogglePending={setMcpConfig.isPending}
          />
        </>
      )}
    </motion.div>
  );
}
