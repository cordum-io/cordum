import { useEffect, useMemo, useState } from "react";
import { Check, ChevronDown, ChevronUp, Copy, Plug2, Power, Shield, TerminalSquare } from "lucide-react";
import { Card, CardDescription, CardHeader, CardTitle } from "../components/ui/Card";
import { Badge } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import {
  useCreateApiKey,
  useMcpConfig,
  useMcpResources,
  useMcpStatus,
  useMcpTools,
  useSetMcpConfig,
} from "../hooks/useSettings";
import { usePageTitle } from "../hooks/usePageTitle";
import type { McpConfig, McpTransport } from "../api/types";

function parseOrigins(value: string): string[] {
  return Array.from(new Set(value.split(",").map((item) => item.trim()).filter(Boolean)));
}

function stringifyOrigins(origins: string[]): string {
  return origins.join(", ");
}

function maskKey(value: string): string {
  if (!value) return "Not configured";
  if (value.startsWith("****")) return value;
  if (value.length <= 4) return "****";
  return `****${value.slice(-4)}`;
}

function formatUptime(seconds: number): string {
  if (!seconds || seconds <= 0) return "0m";
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}

function resourceConfigKey(uri: string, fallbackName: string): string {
  const value = uri.toLowerCase();
  if (value.includes("://jobs")) return "jobs";
  if (value.includes("://workflows")) return "workflows";
  if (value.includes("://audit")) return "audit";
  if (value.includes("://health")) return "health";
  if (value.includes("://policies")) return "policies";
  return fallbackName.toLowerCase().replace(/\s+/g, "_");
}

function ToggleSwitch({
  checked,
  onChange,
  label,
  description,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  label: string;
  description: string;
}) {
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-2xl border border-border p-4">
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={`relative inline-flex h-6 w-11 shrink-0 rounded-full border-2 border-transparent transition-colors ${
          checked ? "bg-accent" : "bg-surface2"
        }`}
      >
        <span
          className={`pointer-events-none inline-block h-5 w-5 rounded-full bg-white shadow-sm transition-transform ${
            checked ? "translate-x-5" : "translate-x-0"
          }`}
        />
      </button>
      <div>
        <p className="text-sm font-semibold text-ink">{label}</p>
        <p className="text-xs text-muted">{description}</p>
      </div>
    </div>
  );
}

const TRANSPORT_OPTIONS: Array<{ value: McpTransport; label: string; description: string }> = [
  { value: "http", label: "HTTP + SSE", description: "Remote clients over HTTP/SSE (recommended)" },
  { value: "stdio", label: "stdio", description: "Local process transport for CLI integrations" },
  { value: "both", label: "Both", description: "Enable HTTP/SSE and stdio simultaneously" },
];

export default function SettingsMcpPage() {
  usePageTitle("Settings - MCP Server");

  const { data: mcpConfig, isLoading } = useMcpConfig();
  const { data: status } = useMcpStatus();
  const { data: tools = [] } = useMcpTools();
  const { data: resources = [] } = useMcpResources();
  const setMcpConfig = useSetMcpConfig();
  const createApiKey = useCreateApiKey();

  const [draft, setDraft] = useState<McpConfig | null>(null);
  const [transportOpen, setTransportOpen] = useState(false);
  const [expandedSchemaTool, setExpandedSchemaTool] = useState<string | null>(null);
  const [generatedSecret, setGeneratedSecret] = useState("");
  const [copiedKey, setCopiedKey] = useState(false);
  const [copiedDesktopSnippet, setCopiedDesktopSnippet] = useState(false);
  const [copiedCliSnippet, setCopiedCliSnippet] = useState(false);

  useEffect(() => {
    if (mcpConfig) {
      setDraft(mcpConfig);
    }
  }, [mcpConfig]);

  const activeConfig = draft ?? mcpConfig;
  const serverURL = `http://localhost:${activeConfig?.port ?? 3001}`;
  const keyDisplay = maskKey(generatedSecret || activeConfig?.apiKeyMasked || "");
  const toolRows = useMemo(
    () =>
      tools.map((tool) => ({
        ...tool,
        enabled: activeConfig?.tools[tool.name]?.enabled ?? tool.enabled,
      })),
    [tools, activeConfig],
  );
  const resourceRows = useMemo(
    () =>
      resources.map((resource) => ({
        ...resource,
        configKey: resourceConfigKey(resource.uri, resource.name),
        enabled:
          activeConfig?.resources[resourceConfigKey(resource.uri, resource.name)]?.enabled ??
          resource.enabled,
      })),
    [resources, activeConfig],
  );

  const desktopConfigSnippet = useMemo(() => {
    return JSON.stringify(
      {
        mcpServers: {
          cordum: {
            command: "cordum-mcp",
            args: ["--addr", serverURL],
            env: {
              CORDUM_API_KEY: generatedSecret || "replace-with-api-key",
            },
          },
        },
      },
      null,
      2,
    );
  }, [generatedSecret, serverURL]);

  const cliSnippet = `claude mcp add cordum -- cordum-mcp --addr ${serverURL}`;

  const updateAndPersist = (patch: Partial<McpConfig>) => {
    setDraft((current) => (current ? { ...current, ...patch } : current));
    setMcpConfig.mutate(patch);
  };

  const saveTransport = () => {
    if (!activeConfig) return;
    updateAndPersist({
      transport: activeConfig.transport,
      port: activeConfig.port,
      allowedOrigins: activeConfig.allowedOrigins,
    });
  };

  const saveAuth = () => {
    if (!activeConfig) return;
    updateAndPersist({
      requireAuth: activeConfig.requireAuth,
    });
  };

  const toggleTool = (toolName: string, enabled: boolean) => {
    if (!activeConfig) return;
    updateAndPersist({
      tools: {
        ...activeConfig.tools,
        [toolName]: { enabled },
      },
    });
  };

  const toggleResource = (key: string, enabled: boolean) => {
    if (!activeConfig) return;
    updateAndPersist({
      resources: {
        ...activeConfig.resources,
        [key]: { enabled },
      },
    });
  };

  const generateMcpKey = async () => {
    const result = await createApiKey.mutateAsync({
      name: "MCP Server Key",
      scopes: ["mcp"],
    });
    setGeneratedSecret(result.secret);
  };

  const copyText = async (
    value: string,
    onCopied: (next: boolean) => void,
  ) => {
    if (!value) return;
    await navigator.clipboard.writeText(value);
    onCopied(true);
    setTimeout(() => onCopied(false), 1500);
  };

  if (isLoading || !activeConfig) {
    return (
      <div className="space-y-4">
        {Array.from({ length: 3 }, (_, idx) => (
          <div key={idx} className="h-40 animate-pulse rounded-2xl bg-surface2" />
        ))}
      </div>
    );
  }

  return (
    <div className="space-y-6 pb-12">
      <Card>
        <CardHeader className="flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <CardTitle>MCP Server</CardTitle>
            <CardDescription>
              Expose Cordum as an MCP server for Claude Desktop and Claude Code integration.
            </CardDescription>
          </div>
          <div className="flex items-center gap-2 rounded-full border border-border px-3 py-1.5">
            <span
              className={`h-2.5 w-2.5 rounded-full ${
                status?.running ? "bg-success" : "bg-muted"
              }`}
            />
            <span className="text-xs font-medium text-ink">
              {status?.running ? `Running (${status.connectedClients} clients)` : "Stopped"}
            </span>
            <Badge variant="default">{formatUptime(status?.uptime ?? 0)}</Badge>
          </div>
        </CardHeader>

        <ToggleSwitch
          checked={activeConfig.enabled}
          onChange={(next) => updateAndPersist({ enabled: next })}
          label="Enable MCP Server"
          description="Allow MCP clients to connect to Cordum using configured transport and auth settings."
        />
      </Card>

      <Card>
        <CardHeader className="flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <CardTitle>Transport Configuration</CardTitle>
            <CardDescription>Choose how MCP clients connect to Cordum.</CardDescription>
          </div>
          <Button
            variant="ghost"
            size="sm"
            type="button"
            onClick={() => setTransportOpen((value) => !value)}
          >
            {transportOpen ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}
            {transportOpen ? "Collapse" : "Configure"}
          </Button>
        </CardHeader>

        {transportOpen && (
          <div className="space-y-4">
            <div className="grid grid-cols-1 gap-2 md:grid-cols-3">
              {TRANSPORT_OPTIONS.map((option) => (
                <label
                  key={option.value}
                  className={`cursor-pointer rounded-xl border p-3 text-sm transition ${
                    activeConfig.transport === option.value
                      ? "border-accent bg-accent/10"
                      : "border-border hover:border-accent/40"
                  }`}
                >
                  <input
                    type="radio"
                    name="mcp-transport"
                    className="sr-only"
                    checked={activeConfig.transport === option.value}
                    onChange={() =>
                      setDraft((current) => (current ? { ...current, transport: option.value } : current))
                    }
                  />
                  <p className="font-semibold text-ink">{option.label}</p>
                  <p className="text-xs text-muted">{option.description}</p>
                </label>
              ))}
            </div>

            {(activeConfig.transport === "http" || activeConfig.transport === "both") && (
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                <div className="space-y-1">
                  <label className="text-xs font-semibold text-muted">HTTP Port</label>
                  <Input
                    type="number"
                    min={1}
                    max={65535}
                    value={String(activeConfig.port)}
                    onChange={(event) =>
                      setDraft((current) =>
                        current
                          ? {
                              ...current,
                              port: Math.max(1, Math.min(65535, Number.parseInt(event.target.value || "0", 10) || 3001)),
                            }
                          : current,
                      )
                    }
                  />
                </div>
                <div className="space-y-1">
                  <label className="text-xs font-semibold text-muted">Allowed Origins (CORS)</label>
                  <Input
                    value={stringifyOrigins(activeConfig.allowedOrigins)}
                    onChange={(event) =>
                      setDraft((current) =>
                        current
                          ? { ...current, allowedOrigins: parseOrigins(event.target.value) }
                          : current,
                      )
                    }
                    placeholder="https://claude.ai, http://localhost:5173"
                  />
                </div>
              </div>
            )}

            <div className="flex items-center gap-2">
              <Button type="button" onClick={saveTransport} disabled={setMcpConfig.isPending}>
                {setMcpConfig.isPending ? "Saving..." : "Save Transport Settings"}
              </Button>
              <Badge variant="info">{serverURL}</Badge>
            </div>
          </div>
        )}
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Authentication</CardTitle>
          <CardDescription>Manage MCP API key requirements and key lifecycle.</CardDescription>
        </CardHeader>

        <div className="space-y-4">
          <ToggleSwitch
            checked={activeConfig.requireAuth}
            onChange={(next) =>
              setDraft((current) => (current ? { ...current, requireAuth: next } : current))
            }
            label="Require API Key"
            description="When enabled, MCP requests must include a valid API key."
          />

          <div className="rounded-xl border border-border bg-surface2/30 p-3">
            <p className="text-xs font-semibold text-muted">Current MCP API Key</p>
            <div className="mt-2 flex items-center gap-2">
              <code className="flex-1 rounded-lg bg-surface2 px-3 py-2 font-mono text-xs text-ink">
                {keyDisplay}
              </code>
              <Button
                variant="outline"
                size="sm"
                type="button"
                onClick={() =>
                  copyText(generatedSecret || activeConfig.apiKeyMasked || "", setCopiedKey)
                }
                disabled={!(generatedSecret || activeConfig.apiKeyMasked)}
              >
                {copiedKey ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                {copiedKey ? "Copied" : "Copy"}
              </Button>
            </div>
            {generatedSecret && (
              <p className="mt-2 text-xs text-warning">
                New key generated. Copy it now; it will not be shown again.
              </p>
            )}
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              onClick={generateMcpKey}
              disabled={createApiKey.isPending}
            >
              <Shield className="h-3.5 w-3.5" />
              {createApiKey.isPending ? "Generating..." : "Generate MCP API Key"}
            </Button>
            <Button
              variant="outline"
              type="button"
              onClick={saveAuth}
              disabled={setMcpConfig.isPending}
            >
              <Power className="h-3.5 w-3.5" />
              Save Auth Settings
            </Button>
          </div>
        </div>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Quick Start</CardTitle>
          <CardDescription>
            Copy a ready-to-use snippet for Claude Desktop or Claude Code.
          </CardDescription>
        </CardHeader>

        <div className="space-y-4">
          <div className="space-y-2 rounded-xl border border-border p-3">
            <div className="flex items-center justify-between">
              <p className="text-sm font-semibold text-ink">Claude Desktop Config</p>
              <Button
                variant="outline"
                size="sm"
                type="button"
                onClick={() => copyText(desktopConfigSnippet, setCopiedDesktopSnippet)}
              >
                {copiedDesktopSnippet ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                {copiedDesktopSnippet ? "Copied" : "Copy"}
              </Button>
            </div>
            <pre className="max-h-64 overflow-auto rounded-lg bg-surface2 p-3 text-[11px] text-ink">
              {desktopConfigSnippet}
            </pre>
          </div>

          <div className="space-y-2 rounded-xl border border-border p-3">
            <div className="flex items-center justify-between">
              <p className="text-sm font-semibold text-ink">Claude Code Command</p>
              <Button
                variant="outline"
                size="sm"
                type="button"
                onClick={() => copyText(cliSnippet, setCopiedCliSnippet)}
              >
                {copiedCliSnippet ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
                {copiedCliSnippet ? "Copied" : "Copy"}
              </Button>
            </div>
            <code className="block rounded-lg bg-surface2 px-3 py-2 font-mono text-xs text-ink">
              {cliSnippet}
            </code>
          </div>

          <div className="flex items-center gap-2 text-xs text-muted">
            <Plug2 className="h-3.5 w-3.5" />
            Transport: <span className="font-semibold text-ink">{activeConfig.transport.toUpperCase()}</span>
            <TerminalSquare className="ml-2 h-3.5 w-3.5" />
            Port: <span className="font-semibold text-ink">{activeConfig.port}</span>
          </div>
        </div>
      </Card>

      {/* Tools management */}
      <Card>
        <CardHeader className="flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <CardTitle>Tools</CardTitle>
            <CardDescription>MCP tools exposed to connected clients.</CardDescription>
          </div>
          <Badge variant="info">{toolRows.filter((t) => t.enabled).length}/{toolRows.length} enabled</Badge>
        </CardHeader>

        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="border-b border-border">
              <tr>
                <th className="px-4 py-2 text-left text-xs font-semibold uppercase tracking-wider text-muted">Name</th>
                <th className="px-4 py-2 text-left text-xs font-semibold uppercase tracking-wider text-muted">Description</th>
                <th className="px-4 py-2 text-center text-xs font-semibold uppercase tracking-wider text-muted">Enabled</th>
                <th className="px-4 py-2 text-center text-xs font-semibold uppercase tracking-wider text-muted">Schema</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {toolRows.map((tool) => (
                <tr key={tool.name} className="group">
                  <td className="whitespace-nowrap px-4 py-3 font-mono text-xs text-ink">{tool.name}</td>
                  <td className="px-4 py-3 text-xs text-muted">{tool.description}</td>
                  <td className="px-4 py-3 text-center">
                    <button
                      type="button"
                      role="switch"
                      aria-checked={tool.enabled}
                      onClick={() => toggleTool(tool.name, !tool.enabled)}
                      className={`relative inline-flex h-5 w-9 shrink-0 rounded-full border-2 border-transparent transition-colors ${
                        tool.enabled ? "bg-accent" : "bg-surface2"
                      }`}
                    >
                      <span
                        className={`pointer-events-none inline-block h-4 w-4 rounded-full bg-white shadow-sm transition-transform ${
                          tool.enabled ? "translate-x-4" : "translate-x-0"
                        }`}
                      />
                    </button>
                  </td>
                  <td className="px-4 py-3 text-center">
                    <Button
                      variant="ghost"
                      size="sm"
                      type="button"
                      onClick={() =>
                        setExpandedSchemaTool((prev) => (prev === tool.name ? null : tool.name))
                      }
                    >
                      {expandedSchemaTool === tool.name ? (
                        <ChevronUp className="h-3.5 w-3.5" />
                      ) : (
                        <ChevronDown className="h-3.5 w-3.5" />
                      )}
                    </Button>
                  </td>
                </tr>
              ))}
              {toolRows.length === 0 && (
                <tr>
                  <td colSpan={4} className="px-4 py-6 text-center text-xs text-muted">
                    No tools registered.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        {expandedSchemaTool && (
          <div className="border-t border-border p-4">
            <p className="mb-2 text-xs font-semibold text-muted">
              Input Schema &mdash; <code className="text-ink">{expandedSchemaTool}</code>
            </p>
            <pre className="max-h-64 overflow-auto rounded-lg bg-surface2 p-3 text-[11px] text-ink">
              {JSON.stringify(
                toolRows.find((t) => t.name === expandedSchemaTool)?.inputSchema ?? {},
                null,
                2,
              )}
            </pre>
          </div>
        )}
      </Card>

      {/* Resources management */}
      <Card>
        <CardHeader className="flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <CardTitle>Resources</CardTitle>
            <CardDescription>MCP resources exposed to connected clients.</CardDescription>
          </div>
          <Badge variant="info">{resourceRows.filter((r) => r.enabled).length}/{resourceRows.length} enabled</Badge>
        </CardHeader>

        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="border-b border-border">
              <tr>
                <th className="px-4 py-2 text-left text-xs font-semibold uppercase tracking-wider text-muted">URI Template</th>
                <th className="px-4 py-2 text-left text-xs font-semibold uppercase tracking-wider text-muted">Name</th>
                <th className="px-4 py-2 text-left text-xs font-semibold uppercase tracking-wider text-muted">Description</th>
                <th className="px-4 py-2 text-center text-xs font-semibold uppercase tracking-wider text-muted">Enabled</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {resourceRows.map((resource) => (
                <tr key={resource.uri} className="group">
                  <td className="whitespace-nowrap px-4 py-3 font-mono text-xs text-ink">{resource.uri}</td>
                  <td className="px-4 py-3 text-xs font-medium text-ink">{resource.name}</td>
                  <td className="px-4 py-3 text-xs text-muted">{resource.description}</td>
                  <td className="px-4 py-3 text-center">
                    <button
                      type="button"
                      role="switch"
                      aria-checked={resource.enabled}
                      onClick={() => toggleResource(resource.configKey, !resource.enabled)}
                      className={`relative inline-flex h-5 w-9 shrink-0 rounded-full border-2 border-transparent transition-colors ${
                        resource.enabled ? "bg-accent" : "bg-surface2"
                      }`}
                    >
                      <span
                        className={`pointer-events-none inline-block h-4 w-4 rounded-full bg-white shadow-sm transition-transform ${
                          resource.enabled ? "translate-x-4" : "translate-x-0"
                        }`}
                      />
                    </button>
                  </td>
                </tr>
              ))}
              {resourceRows.length === 0 && (
                <tr>
                  <td colSpan={4} className="px-4 py-6 text-center text-xs text-muted">
                    No resources registered.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </Card>
    </div>
  );
}
