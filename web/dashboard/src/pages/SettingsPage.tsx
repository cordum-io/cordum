import Card from "../components/Card";
import { useSettingsStore } from "../state/settingsStore";
import { useMutation } from "@tanstack/react-query";
import { fetchWorkers } from "../lib/api";
import { useAuthStore } from "../state/authStore";
import { useStreamStore } from "../state/streamStore";

export default function SettingsPage() {
  const apiBase = useSettingsStore((s) => s.apiBase);
  const wsBase = useSettingsStore((s) => s.wsBase);
  const apiKey = useSettingsStore((s) => s.apiKey);
  const setApiBase = useSettingsStore((s) => s.setApiBase);
  const setWsBase = useSettingsStore((s) => s.setWsBase);
  const setApiKey = useSettingsStore((s) => s.setApiKey);
  const reset = useSettingsStore((s) => s.reset);
  const authStatus = useAuthStore((s) => s.status);
  const wsStatus = useStreamStore((s) => s.status);

  const testApiM = useMutation({
    mutationFn: fetchWorkers,
  });

  return (
    <div className="space-y-6">
      <Card title="Settings">
        <div className="grid grid-cols-1 gap-4 text-sm">
          <label className="space-y-1">
            <div className="text-xs text-zinc-500">API Base</div>
            <input
              value={apiBase}
              onChange={(e) => setApiBase(e.target.value)}
              className="w-full rounded-xl border border-white/10 bg-black/20 px-3 py-2 text-sm text-zinc-100"
              placeholder="http://localhost:8081"
            />
          </label>
          <label className="space-y-1">
            <div className="text-xs text-zinc-500">WS Base</div>
            <input
              value={wsBase}
              onChange={(e) => setWsBase(e.target.value)}
              className="w-full rounded-xl border border-white/10 bg-black/20 px-3 py-2 text-sm text-zinc-100"
              placeholder="ws://localhost:8081/api/v1/stream"
            />
          </label>
          <label className="space-y-1">
            <div className="text-xs text-zinc-500">API Key</div>
            <input
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              className="w-full rounded-xl border border-white/10 bg-black/20 px-3 py-2 text-sm text-zinc-100"
              placeholder="Required if gateway enforces API key"
            />
            <div className="text-xs text-zinc-500">
              If the gateway runs with `CORETEX_API_KEY` / `CORETEX_SUPER_SECRET_API_TOKEN` / `API_KEY`, paste the same value
              here.
            </div>
          </label>
        </div>

        <div className="mt-6 flex flex-wrap items-center gap-2">
          <button
            onClick={() => testApiM.mutate()}
            className="rounded-xl border border-white/10 bg-black/20 px-3 py-2 text-xs text-zinc-200 hover:bg-black/30"
          >
            Test API Auth
          </button>
          <button
            onClick={reset}
            className="rounded-xl border border-white/10 bg-black/20 px-3 py-2 text-xs text-zinc-200 hover:bg-black/30"
          >
            Reset to defaults
          </button>
        </div>

        <div className="mt-4 grid grid-cols-3 gap-3 text-sm">
          <div className="rounded-xl border border-white/10 bg-black/20 p-3">
            <div className="text-xs text-zinc-500">Auth</div>
            <div className="mt-1 font-mono text-xs text-zinc-200">{authStatus}</div>
          </div>
          <div className="rounded-xl border border-white/10 bg-black/20 p-3">
            <div className="text-xs text-zinc-500">WS</div>
            <div className="mt-1 font-mono text-xs text-zinc-200">{wsStatus}</div>
          </div>
          <div className="rounded-xl border border-white/10 bg-black/20 p-3">
            <div className="text-xs text-zinc-500">Test</div>
            <div className="mt-1 text-xs text-zinc-200">
              {testApiM.isPending
                ? "checking…"
                : testApiM.isSuccess
                  ? `ok (${testApiM.data.length} workers)`
                  : testApiM.isError
                    ? (testApiM.error instanceof Error ? testApiM.error.message : "failed")
                    : "-"}
            </div>
          </div>
        </div>
      </Card>
    </div>
  );
}
