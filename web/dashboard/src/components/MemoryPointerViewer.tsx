import { useQuery } from "@tanstack/react-query";
import { fetchMemoryPointer } from "../lib/api";
import { useAuthStore } from "../state/authStore";
import EmptyState from "./EmptyState";
import JsonViewer from "./JsonViewer";
import Loading from "./Loading";

export default function MemoryPointerViewer({ pointer }: { pointer: string }) {
  const authStatus = useAuthStore((s) => s.status);
  const canQuery = authStatus === "unknown" || authStatus === "authorized";

  const q = useQuery({
    queryKey: ["memory", pointer, authStatus],
    queryFn: () => fetchMemoryPointer(pointer),
    enabled: Boolean(pointer) && canQuery,
    retry: 0,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });

  if (!pointer) {
    return <EmptyState title="Missing pointer" />;
  }

  if (!canQuery) {
    return (
      <EmptyState
        title="Unauthorized"
        description={
          authStatus === "missing_api_key"
            ? "Gateway requires an API key. Set it in Settings."
            : "API key was rejected. Update it in Settings."
        }
      />
    );
  }

  if (q.isLoading) {
    return <Loading label="Loading Redis value..." />;
  }

  if (q.isError) {
    const msg = q.error instanceof Error ? q.error.message : "Request failed";
    const description = msg.includes("404")
      ? "Not found in Redis (missing/expired). If the job is still running, try again once it completes."
      : msg.includes("Failed to fetch")
      ? "Failed to connect to the API. Make sure the API is running and the API Base URL is configured correctly in Settings."
      : msg;
    return <EmptyState title="Failed to load pointer" description={description} />;
  }

  const value = q.data!;

  return (
    <div className="space-y-3">
      <div className="rounded-xl border border-white/10 bg-black/20 p-3">
        <div className="text-xs text-zinc-500">Pointer</div>
        <div className="mt-1 break-all font-mono text-xs text-zinc-200">{value.pointer}</div>
        <div className="mt-2 grid grid-cols-1 gap-2 text-xs text-zinc-300">
          <div className="flex items-center justify-between gap-3">
            <span className="text-zinc-500">kind</span>
            <span className="font-mono">{value.kind}</span>
          </div>
          <div className="flex items-center justify-between gap-3">
            <span className="text-zinc-500">key</span>
            <span className="break-all font-mono">{value.key}</span>
          </div>
          <div className="flex items-center justify-between gap-3">
            <span className="text-zinc-500">bytes</span>
            <span className="font-mono">{value.size_bytes}</span>
          </div>
        </div>
      </div>

      {value.json !== undefined ? (
        <div className="space-y-2">
          <div className="text-xs text-zinc-500">JSON</div>
          <JsonViewer value={value.json} />
        </div>
      ) : value.text ? (
        <div className="space-y-2">
          <div className="text-xs text-zinc-500">Text</div>
          <pre className="overflow-auto rounded-xl border border-white/10 bg-black/30 p-3 text-xs text-zinc-200">
            {value.text}
          </pre>
        </div>
      ) : (
        <div className="space-y-2">
          <div className="text-xs text-zinc-500">Base64</div>
          <pre className="overflow-auto rounded-xl border border-white/10 bg-black/30 p-3 text-xs text-zinc-200">
            {value.base64}
          </pre>
        </div>
      )}
    </div>
  );
}
