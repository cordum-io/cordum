import { useState, useCallback, useMemo } from "react";
import { Copy, Check, Search, Loader, Database } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import { Input } from "../components/ui/Input";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";

// ---------------------------------------------------------------------------
// Fetch context
// ---------------------------------------------------------------------------

function useContextData(pointer: string) {
  return useQuery<unknown>({
    queryKey: ["context", pointer],
    queryFn: () => get<unknown>(`/memory?ptr=${encodeURIComponent(pointer)}`),
    enabled: !!pointer,
    staleTime: 30_000,
    retry: 1,
  });
}

// ---------------------------------------------------------------------------
// JSON viewer
// ---------------------------------------------------------------------------

function JsonViewer({
  data,
  filter,
}: {
  data: unknown;
  filter: string;
}) {
  const formatted = useMemo(() => JSON.stringify(data, null, 2), [data]);
  const lowerFilter = filter.toLowerCase();

  const lines = useMemo(() => formatted.split("\n"), [formatted]);

  return (
    <pre className="max-h-[60vh] overflow-auto rounded-xl bg-surface2/50 p-4 text-xs leading-relaxed font-mono">
      {lines.map((line, i) => {
        const highlighted =
          lowerFilter && line.toLowerCase().includes(lowerFilter);
        return (
          <div
            key={i}
            className={highlighted ? "bg-accent/15 -mx-4 px-4" : undefined}
          >
            {line}
          </div>
        );
      })}
    </pre>
  );
}

// ---------------------------------------------------------------------------
// ContextPage
// ---------------------------------------------------------------------------

export default function ContextPage() {
  const [input, setInput] = useState("");
  const [pointer, setPointer] = useState("");
  const [filter, setFilter] = useState("");
  const [copied, setCopied] = useState(false);

  const { data, isLoading, isError, error } = useContextData(pointer);

  const handleFetch = useCallback(() => {
    const trimmed = input.trim();
    if (!trimmed) return;
    setPointer(trimmed);
    setFilter("");
    setCopied(false);
  }, [input]);

  const handleCopy = useCallback(() => {
    if (data == null) return;
    navigator.clipboard.writeText(JSON.stringify(data, null, 2)).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }, [data]);

  const jsonSize = useMemo(() => {
    if (data == null) return null;
    const bytes = new Blob([JSON.stringify(data)]).size;
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }, [data]);

  return (
    <div className="space-y-6">
      <h1 className="font-display text-2xl font-bold text-ink">
        Context Inspector
      </h1>

      {/* Input + Fetch */}
      <div className="flex gap-2">
        <div className="flex-1">
          <Input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="Enter job ID or context pointer (e.g. ctx:job:abc123)"
            onKeyDown={(e) => e.key === "Enter" && handleFetch()}
          />
        </div>
        <Button onClick={handleFetch} disabled={!input.trim()}>
          <Database className="mr-1.5 h-4 w-4" />
          Fetch
        </Button>
      </div>

      {/* Empty state */}
      {!pointer && (
        <p className="py-12 text-center text-sm text-muted">
          Enter a job ID or context pointer above to inspect stored context data.
        </p>
      )}

      {/* Loading */}
      {isLoading && (
        <div className="flex items-center justify-center py-12 text-sm text-muted">
          <Loader className="mr-2 h-4 w-4 animate-spin" />
          Fetching context...
        </div>
      )}

      {/* Error */}
      {isError && (
        <Card>
          <p className="py-4 text-center text-sm text-danger">
            {error instanceof Error ? error.message : "Failed to fetch context data."}
          </p>
        </Card>
      )}

      {/* Data */}
      {data != null && !isLoading && (
        <div className="space-y-3">
          {/* Metadata + actions bar */}
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-4 text-xs text-muted">
              <span>
                Pointer: <span className="font-mono text-ink">{pointer}</span>
              </span>
              {jsonSize && (
                <span>
                  Size: <span className="font-mono text-ink">{jsonSize}</span>
                </span>
              )}
            </div>
            <div className="flex items-center gap-2">
              <div className="relative">
                <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted" />
                <Input
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                  placeholder="Filter keys/values..."
                  className="h-8 pl-8 text-xs"
                />
              </div>
              <Button variant="outline" size="sm" onClick={handleCopy}>
                {copied ? (
                  <>
                    <Check className="mr-1 h-3.5 w-3.5" />
                    Copied
                  </>
                ) : (
                  <>
                    <Copy className="mr-1 h-3.5 w-3.5" />
                    Copy JSON
                  </>
                )}
              </Button>
            </div>
          </div>

          {/* JSON tree */}
          <JsonViewer data={data} filter={filter} />
        </div>
      )}
    </div>
  );
}
