import { Globe, Plug, Wrench, Copy, Power } from "lucide-react";
import type { McpConfig, McpResource, McpStatus, McpTool } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { CollapsibleSection } from "@/components/ui/CollapsibleSection";
import { EmptyState } from "@/components/ui/EmptyState";
import { InstrumentCard, InstrumentCardBody, InstrumentCardFooter } from "@/components/ui/InstrumentCard";
import { StatusBadge } from "@/components/ui/StatusBadge";

interface McpServerPanelProps {
  config: McpConfig;
  status?: McpStatus;
  tools: McpTool[];
  resources: McpResource[];
  serverUrl: string;
  uptimeLabel: string;
  onCopyUrl: () => void;
  onToggleRuntime?: () => void;
  runtimeTogglePending?: boolean;
}

function DetailItem({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="min-w-0">
      <p className="text-xs font-mono uppercase tracking-widest text-muted-foreground">{label}</p>
      <p className="mt-1 text-sm font-medium text-foreground">{value}</p>
    </div>
  );
}

function ToolList({ tools }: { tools: McpTool[] }) {
  if (tools.length === 0) {
    return (
      <EmptyState
        icon={<Wrench className="h-5 w-5" />}
        title="No tools registered"
        description="Tools appear here once the MCP server exposes them."
        className="py-8"
      />
    );
  }

  return (
    <div className="space-y-2">
      {tools.map((tool) => (
        <div key={tool.name} className="list-row flex items-start gap-3">
          <div className="mt-1 rounded-xl bg-cordum/10 p-2 text-cordum">
            <Wrench className="h-3.5 w-3.5" />
          </div>
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium text-foreground">{tool.name}</p>
            <p className="mt-1 text-xs text-muted-foreground">{tool.description}</p>
          </div>
          <StatusBadge variant={tool.enabled ? "healthy" : "muted"}>
            {tool.enabled ? "Enabled" : "Disabled"}
          </StatusBadge>
        </div>
      ))}
    </div>
  );
}

function ResourceList({ resources }: { resources: McpResource[] }) {
  if (resources.length === 0) {
    return (
      <EmptyState
        icon={<Globe className="h-5 w-5" />}
        title="No resources registered"
        description="Resources will appear here when the MCP server publishes them."
        className="py-8"
      />
    );
  }

  return (
    <div className="space-y-2">
      {resources.map((resource) => (
        <div key={resource.uri} className="list-row flex items-start gap-3">
          <div className="mt-1 rounded-xl bg-info/10 p-2 text-info">
            <Globe className="h-3.5 w-3.5" />
          </div>
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium text-foreground">{resource.name}</p>
            <p className="mt-1 break-all text-xs text-muted-foreground">{resource.uri}</p>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-xs font-mono text-muted-foreground">{resource.mimeType}</span>
            <StatusBadge variant={resource.enabled ? "healthy" : "muted"}>
              {resource.enabled ? "Enabled" : "Disabled"}
            </StatusBadge>
          </div>
        </div>
      ))}
    </div>
  );
}

export function McpServerPanel({
  config,
  status,
  tools,
  resources,
  serverUrl,
  uptimeLabel,
  onCopyUrl,
  onToggleRuntime,
  runtimeTogglePending = false,
}: McpServerPanelProps) {
  const connected = Boolean(status?.running);

  return (
    <InstrumentCard accent={connected ? "healthy" : "muted"} className="overflow-hidden p-5">
      <InstrumentCardBody>
        <CollapsibleSection
          title="Cordum MCP Server"
          description={serverUrl}
          leading={<Plug className="h-4 w-4 text-cordum" />}
          trailing={
            <div className="hidden items-center gap-4 md:flex">
              <div className="text-right text-xs">
                <p className="font-mono text-foreground">{status?.connectedClients ?? 0} clients</p>
                <p className="text-muted-foreground">{config.transport} transport</p>
              </div>
              <StatusBadge variant={connected ? "healthy" : "muted"} dot pulse={connected}>
                {connected ? "Connected" : "Disconnected"}
              </StatusBadge>
            </div>
          }
          contentClassName="space-y-6"
        >
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
            <DetailItem label="Transport" value={config.transport} />
            <DetailItem label="Port" value={config.port} />
            <DetailItem label="Auth required" value={config.requireAuth ? "Yes" : "No"} />
            <DetailItem label="Uptime" value={uptimeLabel} />
          </div>

          <section className="space-y-3" aria-label="Registered MCP tools">
            <div className="flex items-center justify-between gap-3">
              <div>
                <h2 className="text-sm font-semibold text-foreground">Tools</h2>
                <p className="text-xs text-muted-foreground">
                  {tools.length} registered · {tools.filter((tool) => tool.enabled).length} enabled
                </p>
              </div>
            </div>
            <ToolList tools={tools} />
          </section>

          <section className="space-y-3" aria-label="Registered MCP resources">
            <div>
              <h2 className="text-sm font-semibold text-foreground">Resources</h2>
              <p className="text-xs text-muted-foreground">
                {resources.length} published
              </p>
            </div>
            <ResourceList resources={resources} />
          </section>
        </CollapsibleSection>
      </InstrumentCardBody>

      <InstrumentCardFooter className="flex flex-wrap items-center gap-2">
        {onToggleRuntime && (
          <Button
            variant={config.enabled ? "outline" : "primary"}
            size="sm"
            onClick={onToggleRuntime}
            disabled={runtimeTogglePending}
            loading={runtimeTogglePending}
          >
            <Power className="mr-1 h-3 w-3" />
            {config.enabled ? "Disable runtime" : "Enable runtime"}
          </Button>
        )}
        <Button variant="ghost" size="sm" onClick={onCopyUrl}>
          <Copy className="mr-1 h-3 w-3" />
          Copy URL
        </Button>
      </InstrumentCardFooter>
    </InstrumentCard>
  );
}
