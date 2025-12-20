import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { Activity, PanelRight, PlugZap, Search } from "lucide-react";
import { fetchHealth } from "../lib/api";
import { useStreamStore } from "../state/streamStore";
import { useCommandPaletteStore } from "../state/commandPaletteStore";
import { useInspectorStore } from "../state/inspectorStore";
import { useSettingsStore } from "../state/settingsStore";
import { useAuthStore } from "../state/authStore";

function TrafficLights() {
  return (
    <div className="flex items-center gap-2">
      <div className="h-3 w-3 rounded-full bg-red-500/80" />
      <div className="h-3 w-3 rounded-full bg-yellow-500/80" />
      <div className="h-3 w-3 rounded-full bg-green-500/80" />
    </div>
  );
}

export default function TopBar() {
  const wsStatus = useStreamStore((s) => s.status);
  const openPalette = useCommandPaletteStore((s) => s.openPalette);
  const inspectorOpen = useInspectorStore((s) => s.open);
  const toggleInspector = useInspectorStore((s) => s.toggle);
  const apiBase = useSettingsStore((s) => s.apiBase);
  const authStatus = useAuthStore((s) => s.status);
  const { data, isError, isLoading } = useQuery({
    queryKey: ["health"],
    queryFn: fetchHealth,
    refetchInterval: 5_000,
  });

  const restStatus = isLoading
    ? "checking"
    : isError
      ? "down"
      : data === "ok"
        ? "up"
        : "unknown";

  let envLabel = "custom";
  try {
    const u = new URL(apiBase);
    envLabel = u.hostname === "localhost" || u.hostname === "127.0.0.1" ? "local" : u.hostname;
  } catch {
    // ignore
  }

  return (
    <header className="glass h-[52px] border-b border-primary-border">
      <div className="mx-auto flex h-full max-w-[1400px] items-center justify-between px-4">
        <div className="flex items-center gap-3">
          <TrafficLights />
          <Link to="/dashboard" className="text-sm font-semibold tracking-wide">
            coretexOS Studio
          </Link>
        </div>

        <button
          type="button"
          onClick={openPalette}
          className="mx-6 hidden w-[520px] items-center gap-2 rounded-lg border border-primary-border bg-secondary-background/80 px-4 py-2 text-left text-sm text-secondary-text hover:bg-tertiary-background/80 md:flex"
        >
          <Search className="h-4 w-4 text-tertiary-text" />
          <span className="flex-1 truncate">Command palette…</span>
          <span className="rounded-md border border-secondary-border bg-tertiary-background px-2 py-0.5 text-xs">
            ⌘K
          </span>
        </button>

        <div className="flex items-center gap-2 text-xs text-primary-text">
          <span className="inline-flex items-center gap-2 rounded-full border border-primary-border bg-secondary-background/80 px-3 py-1">
            <span className="text-tertiary-text">env</span>
            <span className="font-mono">{envLabel}</span>
          </span>
          {authStatus === "missing_api_key" || authStatus === "invalid_api_key" ? (
            <Link
              to="/settings"
              className="inline-flex items-center gap-2 rounded-full border border-red-500/30 bg-red-500/10 px-3 py-1 text-red-200 hover:bg-red-500/20"
              title="Open Settings to set API key"
            >
              <span className="text-[11px] uppercase tracking-wider">Auth</span>
              <span className="font-mono">{authStatus === "missing_api_key" ? "missing key" : "invalid key"}</span>
            </Link>
          ) : null}
          <span className="inline-flex items-center gap-2 rounded-full border border-primary-border bg-secondary-background/80 px-3 py-1">
            <PlugZap className="h-4 w-4" />
            REST: {restStatus}
          </span>
          <span className="inline-flex items-center gap-2 rounded-full border border-primary-border bg-secondary-background/80 px-3 py-1">
            <Activity className="h-4 w-4" />
            WS: {wsStatus}
          </span>
          <button
            type="button"
            onClick={toggleInspector}
            className="inline-flex items-center gap-2 rounded-full border border-primary-border bg-secondary-background/80 px-3 py-1 hover:bg-tertiary-background"
            title="Toggle Inspector"
          >
            <PanelRight className="h-4 w-4" />
            {inspectorOpen ? "Inspector" : "Inspector"}
          </button>
        </div>
      </div>
    </header>
  );
}
