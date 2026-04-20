import { useMemo, useState, type ReactNode } from "react";
import { AlertTriangle, BrainCircuit, Loader2, Search } from "lucide-react";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import { EmptyState } from "../ui/EmptyState";
import { useMemory } from "../../hooks/useMemory";
import type { MemoryEntry } from "../../api/types";

interface MemoryPanelProps {
  memoryPtr?: string;
  jobId: string;
}

const COLLAPSED_LENGTH = 200;

function roleVariant(role: MemoryEntry["role"]): "default" | "info" | "warning" | "success" {
  switch (role) {
    case "user":
      return "info";
    case "assistant":
    case "agent":
      return "success";
    case "system":
    case "tool":
      return "warning";
    default:
      return "default";
  }
}

function roleHelp(role: MemoryEntry["role"]): string {
  switch (role) {
    case "system":
      return "System instructions or runtime context.";
    case "user":
      return "User-provided input or prompt.";
    case "assistant":
      return "Model or assistant-generated output.";
    case "agent":
      return "Agent step output or orchestration note.";
    case "tool":
      return "Tool call payload or tool response.";
    default:
      return "Unclassified memory entry.";
  }
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function highlightText(content: string, filter: string): ReactNode {
  const query = filter.trim();
  if (!query) return content;
  const regex = new RegExp(`(${escapeRegExp(query)})`, "ig");
  const parts = content.split(regex);
  if (parts.length <= 1) return content;
  const normalizedQuery = query.toLowerCase();
  return parts.map((part, index) =>
    part.toLowerCase() === normalizedQuery ? (
      <mark key={`${part}-${index}`} className="rounded bg-accent/20 px-0.5 text-ink">
        {part}
      </mark>
    ) : (
      <span key={`${part}-${index}`}>{part}</span>
    ),
  );
}

function formatTimestamp(raw?: string): string | null {
  if (!raw) return null;
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) return raw;
  return date.toLocaleString();
}

export function MemoryPanel({ memoryPtr, jobId }: MemoryPanelProps) {
  const [filter, setFilter] = useState("");
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const { data, isLoading, isError, error, refetch } = useMemory(memoryPtr);

  const entries = useMemo(() => data?.entries ?? [], [data?.entries]);
  const normalizedFilter = filter.trim().toLowerCase();
  const filteredEntries = useMemo(() => {
    if (!normalizedFilter) return entries;
    return entries.filter((entry) => {
      const haystack = `${entry.role} ${entry.content}`.toLowerCase();
      return haystack.includes(normalizedFilter);
    });
  }, [entries, normalizedFilter]);

  const toggleExpanded = (entryId: string) => {
    setExpanded((current) => ({ ...current, [entryId]: !current[entryId] }));
  };

  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Memory</CardTitle>
          {memoryPtr ? (
            <p className="mt-1 max-w-[32rem] truncate font-mono text-xs text-muted-foreground" title={memoryPtr}>
              {memoryPtr}
            </p>
          ) : (
            <p className="mt-1 text-xs text-muted-foreground">Job {jobId.slice(0, 8)} has no memory pointer.</p>
          )}
        </div>
      </CardHeader>

      {memoryPtr && (
        <div className="mb-4">
          <label className="mb-2 block text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            Search Memory Entries
          </label>
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={filter}
              onChange={(event) => setFilter(event.target.value)}
              placeholder="Find text in memory..."
              className="pl-9"
            />
          </div>
        </div>
      )}

      {!memoryPtr && (
        <EmptyState
          icon={<BrainCircuit className="h-10 w-10" />}
          title="No memory context for this job"
          description="This job was submitted without a memory pointer."
        />
      )}

      {memoryPtr && isLoading && (
        <div className="space-y-3">
          {Array.from({ length: 3 }).map((_, index) => (
            <div key={index} className="rounded-2xl border border-border/60 bg-surface2/20 p-4">
              <div className="mb-3 h-4 w-24 animate-pulse rounded bg-surface2" />
              <div className="space-y-2">
                <div className="h-3 w-11/12 animate-pulse rounded bg-surface2" />
                <div className="h-3 w-8/12 animate-pulse rounded bg-surface2" />
              </div>
            </div>
          ))}
        </div>
      )}

      {memoryPtr && isError && (
        <div className="rounded-2xl border border-danger/40 bg-danger/5 p-4">
          <div className="mb-3 flex items-center gap-2 text-danger">
            <AlertTriangle className="h-4 w-4" />
            <span className="text-sm font-semibold">Failed to load memory entries</span>
          </div>
          <p className="text-xs text-muted-foreground">
            {error instanceof Error ? error.message : "Unknown error"}
          </p>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="mt-3"
            onClick={() => {
              void refetch();
            }}
          >
            <Loader2 className="h-3.5 w-3.5" />
            Retry
          </Button>
        </div>
      )}

      {memoryPtr && !isLoading && !isError && filteredEntries.length === 0 && (
        <EmptyState
          icon={<BrainCircuit className="h-10 w-10" />}
          title={entries.length === 0 ? "No memory context for this job" : "No memory entries match your search"}
          description={
            entries.length === 0
              ? "No conversation entries were found for this memory pointer."
              : "Try a different search term."
          }
        />
      )}

      {memoryPtr && !isLoading && !isError && filteredEntries.length > 0 && (
        <div className="space-y-3">
          {filteredEntries.map((entry) => {
            const expandedEntry = !!expanded[entry.id];
            const shouldCollapse = entry.content.length > COLLAPSED_LENGTH;
            const renderedContent =
              shouldCollapse && !expandedEntry
                ? `${entry.content.slice(0, COLLAPSED_LENGTH)}...`
                : entry.content;
            const timestamp = formatTimestamp(entry.timestamp);
            return (
              <div
                key={entry.id}
                className="rounded-2xl border border-border/60 bg-card/40 p-4 transition hover:border-border"
              >
                <div className="mb-2 flex flex-wrap items-center gap-2">
                  <Badge variant={roleVariant(entry.role)} title={roleHelp(entry.role)}>
                    {entry.role}
                  </Badge>
                  {timestamp && (
                    <span className="text-xs text-muted-foreground">{timestamp}</span>
                  )}
                </div>
                <div className="rounded-xl bg-surface2/35 p-3">
                  <p className="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed text-ink">
                    {highlightText(renderedContent, filter)}
                  </p>
                </div>
                {shouldCollapse && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="mt-2"
                    onClick={() => toggleExpanded(entry.id)}
                  >
                    {expandedEntry ? "Show less" : "Show more"}
                  </Button>
                )}
              </div>
            );
          })}
        </div>
      )}
    </Card>
  );
}
