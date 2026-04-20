/*
 * DESIGN: "Control Surface" — MCP Server
 * MCP server management with tool discovery, analytics, and policy integration
 */
import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { SkeletonCard } from "@/components/ui/Skeleton";
import {
  Server, Plug, RefreshCw, Copy, ChevronDown, ChevronRight,
  Wrench, Shield, BarChart3, AlertTriangle,
  Globe, Zap,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { toast } from "sonner";
import {
  useMcpStatus,
  useMcpConfig,
  useMcpTools,
  useMcpResources,
} from "@/hooks/useSettings";

function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return `${h}h ${m}m`;
}

export default function SettingsMcpPage() {
  const [expandedServer, setExpandedServer] = useState<string | null>(null);
  const [tab, setTab] = useState<"servers" | "analytics">("servers");

  const { data: mcpConfig, isLoading: configLoading } = useMcpConfig();
  const { data: mcpStatus } = useMcpStatus();
  const { data: tools = [], isLoading: toolsLoading } = useMcpTools();
  const { data: resources = [], isLoading: resourcesLoading } = useMcpResources();

  const isLoading = configLoading || toolsLoading || resourcesLoading;

  // Build single server entry representing Cordum MCP
  const serverStatus = mcpStatus?.running ? "connected" : "disconnected";
  const serverUrl = mcpConfig ? `${mcpConfig.transport}://${window.location.hostname}:${mcpConfig.port}` : "";

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader
        title="MCP Servers"
        subtitle={`1 server · ${tools.length} tools · ${resources.length} resources`}
      />

      {isLoading ? (
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
          <SkeletonCard /><SkeletonCard /><SkeletonCard /><SkeletonCard />
        </div>
      ) : !mcpConfig ? (
        <div className="instrument-card p-8 text-center">
          <AlertTriangle className="w-8 h-8 text-red-400 mx-auto mb-3" />
          <p className="text-sm text-foreground font-medium mb-1">Failed to load MCP configuration</p>
          <p className="text-xs text-muted-foreground">Configuration data is unavailable</p>
        </div>
      ) : (
        <>
          {/* Disabled notice */}
          {mcpConfig && !mcpConfig.enabled && (
            <div className="instrument-card p-4 status-warning">
              <div className="flex items-center gap-3">
                <AlertTriangle className="w-4 h-4 text-amber-400" />
                <p className="text-sm text-foreground">MCP server is disabled. Enable it in configuration to allow MCP connections.</p>
              </div>
            </div>
          )}

          {/* KPI Row */}
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
            <div className="instrument-card p-4">
              <div className="flex items-center justify-between mb-2">
                <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Servers</span>
                <Server className="w-4 h-4 text-cordum" />
              </div>
              <span className="font-mono text-2xl font-bold text-foreground">1</span>
              <p className="text-xs text-muted-foreground mt-1">{mcpStatus?.running ? "1" : "0"} connected</p>
            </div>
            <div className="instrument-card p-4">
              <div className="flex items-center justify-between mb-2">
                <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Tools</span>
                <Wrench className="w-4 h-4 text-cordum" />
              </div>
              <span className="font-mono text-2xl font-bold text-foreground">{tools.length}</span>
              <p className="text-xs text-muted-foreground mt-1">{tools.filter(t => t.enabled).length} enabled</p>
            </div>
            <div className="instrument-card p-4">
              <div className="flex items-center justify-between mb-2">
                <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Clients</span>
                <Zap className="w-4 h-4 text-cordum" />
              </div>
              <span className="font-mono text-2xl font-bold text-foreground">{mcpStatus?.connectedClients ?? 0}</span>
              <p className="text-xs text-muted-foreground mt-1">connected clients</p>
            </div>
            <div className="instrument-card p-4">
              <div className="flex items-center justify-between mb-2">
                <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Uptime</span>
                <Shield className="w-4 h-4 text-cordum" />
              </div>
              <span className="font-mono text-2xl font-bold text-foreground">{mcpStatus?.uptime ? formatUptime(mcpStatus.uptime) : "\u2014"}</span>
            </div>
          </div>

          {/* Tabs */}
          <div className="flex items-center gap-4 border-b border-border">
            <button
              onClick={() => setTab("servers")}
              className={cn(
                "pb-2 text-sm font-medium border-b-2 transition-colors",
                tab === "servers" ? "border-cordum text-cordum" : "border-transparent text-muted-foreground hover:text-foreground"
              )}
            >
              <Plug className="w-3.5 h-3.5 inline mr-1.5" />
              Servers (1)
            </button>
            <button
              onClick={() => setTab("analytics")}
              className={cn(
                "pb-2 text-sm font-medium border-b-2 transition-colors",
                tab === "analytics" ? "border-cordum text-cordum" : "border-transparent text-muted-foreground hover:text-foreground"
              )}
            >
              <BarChart3 className="w-3.5 h-3.5 inline mr-1.5" />
              Analytics
            </button>
          </div>

          {tab === "servers" && (
            <div className="space-y-3">
              <motion.div
                initial={{ opacity: 0, y: 8 }}
                animate={{ opacity: 1, y: 0 }}
                className={cn("instrument-card overflow-hidden", serverStatus === "connected" && "status-healthy")}
              >
                <div
                  className="p-5 cursor-pointer hover:bg-surface-1 transition-colors"
                  onClick={() => setExpandedServer(expandedServer === "cordum-mcp" ? null : "cordum-mcp")}
                >
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                      {expandedServer === "cordum-mcp" ? <ChevronDown className="w-4 h-4 text-muted-foreground" /> : <ChevronRight className="w-4 h-4 text-muted-foreground" />}
                      <Plug className="w-4 h-4 text-cordum" />
                      <div>
                        <span className="text-sm font-display font-semibold text-foreground">Cordum MCP Server</span>
                        <p className="text-xs font-mono text-muted-foreground mt-0.5">{serverUrl}</p>
                      </div>
                    </div>
                    <div className="flex items-center gap-4">
                      <div className="text-right text-xs">
                        <p className="font-mono text-foreground">{mcpStatus?.connectedClients ?? 0} clients</p>
                        <p className="text-muted-foreground">{mcpConfig?.transport ?? "http"} transport</p>
                      </div>
                      <StatusBadge variant={serverStatus === "connected" ? "healthy" : "muted"} dot>
                        {serverStatus}
                      </StatusBadge>
                    </div>
                  </div>
                </div>

                <AnimatePresence>
                  {expandedServer === "cordum-mcp" && (
                    <motion.div
                      initial={{ height: 0, opacity: 0 }}
                      animate={{ height: "auto", opacity: 1 }}
                      exit={{ height: 0, opacity: 0 }}
                      className="border-t border-border"
                    >
                      <div className="p-5 space-y-4">
                        {/* Server Info */}
                        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 text-xs">
                          <div>
                            <p className="text-muted-foreground mb-1">Transport</p>
                            <p className="font-mono text-foreground">{mcpConfig?.transport ?? "\u2014"}</p>
                          </div>
                          <div>
                            <p className="text-muted-foreground mb-1">Port</p>
                            <p className="font-mono text-foreground">{mcpConfig?.port ?? "\u2014"}</p>
                          </div>
                          <div>
                            <p className="text-muted-foreground mb-1">Auth Required</p>
                            <p className="font-mono text-foreground">{mcpConfig?.requireAuth ? "Yes" : "No"}</p>
                          </div>
                          <div>
                            <p className="text-muted-foreground mb-1">Uptime</p>
                            <p className="font-mono text-foreground">{mcpStatus?.uptime ? formatUptime(mcpStatus.uptime) : "\u2014"}</p>
                          </div>
                        </div>

                        {/* Tools */}
                        <div>
                          <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest mb-2">Tools ({tools.length})</p>
                          {tools.length === 0 ? (
                            <p className="text-xs text-muted-foreground">No tools registered</p>
                          ) : (
                            <div className="space-y-1.5">
                              {tools.map((tool) => (
                                <div key={tool.name} className="flex items-center gap-3 px-3 py-2 rounded-md bg-surface-1">
                                  <Wrench className="w-3 h-3 text-cordum shrink-0" />
                                  <div className="flex-1 min-w-0">
                                    <span className="text-xs font-mono font-medium text-foreground">{tool.name}</span>
                                    <p className="text-[10px] text-muted-foreground">{tool.description}</p>
                                  </div>
                                  <StatusBadge variant={tool.enabled ? "healthy" : "muted"}>
                                    {tool.enabled ? "enabled" : "disabled"}
                                  </StatusBadge>
                                </div>
                              ))}
                            </div>
                          )}
                        </div>

                        {/* Resources */}
                        {resources.length > 0 && (
                          <div>
                            <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-widest mb-2">Resources ({resources.length})</p>
                            <div className="space-y-1.5">
                              {resources.map((res) => (
                                <div key={res.uri} className="flex items-center gap-3 px-3 py-2 rounded-md bg-surface-1">
                                  <Globe className="w-3 h-3 text-blue-400 shrink-0" />
                                  <div className="flex-1 min-w-0">
                                    <span className="text-xs font-mono font-medium text-foreground">{res.name}</span>
                                    <p className="text-[10px] text-muted-foreground">{res.uri}</p>
                                  </div>
                                  <span className="text-[10px] font-mono text-muted-foreground">{res.mimeType}</span>
                                  <StatusBadge variant={res.enabled ? "healthy" : "muted"}>
                                    {res.enabled ? "enabled" : "disabled"}
                                  </StatusBadge>
                                </div>
                              ))}
                            </div>
                          </div>
                        )}

                        {/* Actions */}
                        <div className="flex gap-2 pt-2 border-t border-border">
                          <Button variant="outline" size="sm" onClick={(e) => { e.stopPropagation(); toast.info("Refreshing tools..."); }}>
                            <RefreshCw className="w-3 h-3 mr-1" />Refresh
                          </Button>
                          <Button variant="ghost" size="sm" onClick={(e) => { e.stopPropagation(); navigator.clipboard.writeText(serverUrl); toast.success("URL copied"); }}>
                            <Copy className="w-3 h-3 mr-1" />Copy URL
                          </Button>
                        </div>
                      </div>
                    </motion.div>
                  )}
                </AnimatePresence>
              </motion.div>
            </div>
          )}

          {tab === "analytics" && (
            <div className="instrument-card p-8 text-center">
              <BarChart3 className="w-8 h-8 text-muted-foreground/30 mx-auto mb-3" />
              <p className="text-sm text-foreground font-medium mb-1">MCP Analytics</p>
              <p className="text-xs text-muted-foreground">
                Call volume analytics are not yet available. Server-side metrics collection will be implemented in a future release.
              </p>
            </div>
          )}
        </>
      )}
    </motion.div>
  );
}
