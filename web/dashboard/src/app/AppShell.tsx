import React, { useEffect } from "react";
import Sidebar from "./Sidebar";
import TopBar from "./TopBar";
import Inspector from "./Inspector";
import CommandPalette from "./CommandPalette";
import { useStreamStore } from "../state/streamStore";
import { useSettingsStore } from "../state/settingsStore";
import { useCommandPaletteStore } from "../state/commandPaletteStore";
import { useAuthStore } from "../state/authStore";

export default function AppShell({ children }: { children: React.ReactNode }) {
  const connect = useStreamStore((s) => s.connect);
  const disconnect = useStreamStore((s) => s.disconnect);
  const wsUrl = useSettingsStore((s) => s.wsBase);
  const apiKey = useSettingsStore((s) => s.apiKey);
  const authStatus = useAuthStore((s) => s.status);
  const openPalette = useCommandPaletteStore((s) => s.openPalette);
  const closePalette = useCommandPaletteStore((s) => s.close);

  useEffect(() => {
    const hasKey = apiKey.trim() !== "";
    const unauthorized = authStatus === "missing_api_key" || authStatus === "invalid_api_key";
    // Only connect WS once we know we are authorized when using an API key.
    // This avoids noisy "closed before established" errors when the key is missing/invalid.
    const shouldConnect = !unauthorized && (!hasKey || authStatus === "authorized");
    if (!shouldConnect) {
      disconnect();
      return;
    }
    connect();
    return () => disconnect();
  }, [connect, disconnect, wsUrl, apiKey, authStatus]);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      const k = e.key.toLowerCase();
      if (k === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        openPalette();
        return;
      }
      if (k === "escape") {
        closePalette();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [closePalette, openPalette]);

  return (
    <div className="h-screen w-screen overflow-hidden bg-gradient-radial from-primary-background to-secondary-background text-primary-text">
      <TopBar />
      <div className="flex h-[calc(100vh-52px)]">
        <Sidebar />
        <main className="flex-1 overflow-auto p-6">{children}</main>
        <Inspector />
      </div>
      <CommandPalette />
    </div>
  );
}
