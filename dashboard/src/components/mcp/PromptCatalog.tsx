// PromptCatalog — renders the MCP Prompt surface the gateway exposes
// via prompts/list. Each card links out to the docs entry so an
// operator evaluating whether the prompt fits their workflow can jump
// straight to the rendered-message shape + safety notes in one click.
//
// Design vocabulary: governance surface-card + amber safety chip
// (same pattern the signature surface uses) + muted model-class chip.
// Matches the existing ToolUsageHeatmap/MCPApprovalQueue visual
// language so the MCP page stays cohesive.

import { useMemo, useState } from "react";
import { Search, BookOpen, AlertTriangle, Cpu, Sparkles } from "lucide-react";
import type { McpPrompt } from "@/api/types";
import { Card, CardTitle } from "@/components/ui/Card";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { cn } from "@/lib/utils";

export interface PromptCatalogProps {
  prompts: McpPrompt[] | undefined;
  isLoading: boolean;
  error: unknown;
  className?: string;
}

export function PromptCatalog({
  prompts,
  isLoading,
  error,
  className,
}: PromptCatalogProps) {
  const [filter, setFilter] = useState("");

  const filtered = useMemo(() => {
    if (!prompts) return [];
    const q = filter.trim().toLowerCase();
    if (!q) return prompts;
    return prompts.filter(
      (p) =>
        p.name.toLowerCase().includes(q) ||
        p.description.toLowerCase().includes(q),
    );
  }, [prompts, filter]);

  if (error) {
    return (
      <ErrorBanner
        title="Unable to load MCP prompts"
        message={
          error instanceof Error
            ? error.message
            : "Unexpected error loading the prompt catalogue."
        }
      />
    );
  }

  return (
    <section
      aria-labelledby="mcp-prompts-heading"
      className={cn("space-y-4", className)}
      data-testid="mcp-prompt-catalog"
    >
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h2
              id="mcp-prompts-heading"
              className="font-display text-lg font-semibold text-ink"
            >
              Prompts
            </h2>
            {!isLoading && prompts && (
              <span className="inline-flex items-center rounded-full border border-border bg-muted px-2 py-0.5 text-[11px] font-semibold text-muted-foreground">
                {prompts.length}
              </span>
            )}
          </div>
          <p className="mt-0.5 text-xs text-muted-foreground">
            Server-side prompt templates MCP clients can request via
            <code className="ml-1 rounded bg-muted px-1 py-0.5 font-mono text-[11px]">prompts/list</code>.
          </p>
        </div>
        <div className="relative">
          <Search
            className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
            aria-hidden="true"
          />
          <input
            type="search"
            placeholder="Filter prompts…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className={cn(
              "h-8 w-56 rounded-full border border-border bg-background pl-8 pr-3 text-xs",
              "placeholder:text-muted-foreground/70",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cordum/40",
            )}
            aria-label="Filter prompts"
            data-testid="mcp-prompt-filter"
          />
        </div>
      </div>

      {isLoading && (
        <div className="grid gap-4 md:grid-cols-2">
          <SkeletonCard />
          <SkeletonCard />
          <SkeletonCard />
          <SkeletonCard />
        </div>
      )}

      {!isLoading && prompts && prompts.length === 0 && (
        <EmptyState
          icon={<Sparkles className="h-5 w-5" />}
          title="No MCP prompts registered"
          description="The gateway's MCP server is running but no prompt templates are wired. Register prompts via RegisterAllPrompts or a custom PromptService."
        />
      )}

      {!isLoading && filtered.length === 0 && (prompts?.length ?? 0) > 0 && (
        <EmptyState
          icon={<Search className="h-5 w-5" />}
          title="No matching prompts"
          description={`No prompt matches “${filter}”. Try a broader term.`}
        />
      )}

      {!isLoading && filtered.length > 0 && (
        <div
          className="grid gap-4 md:grid-cols-2"
          data-testid="mcp-prompt-grid"
        >
          {filtered.map((p) => (
            <PromptCard key={p.name} prompt={p} />
          ))}
        </div>
      )}
    </section>
  );
}

function PromptCard({ prompt }: { prompt: McpPrompt }) {
  const requiredArgs = prompt.arguments.filter((a) => a.required);
  const optionalArgs = prompt.arguments.filter((a) => !a.required);
  return (
    <Card
      className="relative overflow-hidden"
      data-testid={`mcp-prompt-card-${prompt.name}`}
    >
      <span
        aria-hidden="true"
        className={cn(
          "pointer-events-none absolute inset-x-0 top-0 h-0.5",
          prompt.safetyDisclaimer ? "bg-warning" : "bg-cordum/60",
        )}
      />
      <header className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <CardTitle className="font-mono text-sm text-ink">
            {prompt.name}
          </CardTitle>
        </div>
        <div className="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
          <ModelClassChip modelClass={prompt.modelClass} />
          {prompt.safetyDisclaimer && <SafetyDisclaimerChip />}
        </div>
      </header>

      <p className="text-xs text-muted-foreground">{prompt.description}</p>

      <div className="mt-4 space-y-2">
        {requiredArgs.length > 0 && (
          <ArgList label="Required" args={requiredArgs} />
        )}
        {optionalArgs.length > 0 && (
          <ArgList label="Optional" args={optionalArgs} muted />
        )}
      </div>

      <div className="mt-4 flex items-center justify-end">
        <a
          href={prompt.docsHref}
          className={cn(
            "inline-flex items-center gap-1.5 rounded-full border border-border px-2.5 py-1 text-[11px] font-medium text-muted-foreground",
            "transition-colors hover:bg-muted hover:text-ink",
          )}
          rel="noreferrer"
          data-testid={`mcp-prompt-card-${prompt.name}-docs`}
        >
          <BookOpen className="h-3 w-3" aria-hidden="true" />
          Read the docs
        </a>
      </div>
    </Card>
  );
}

function ArgList({
  label,
  args,
  muted,
}: {
  label: string;
  args: McpPrompt["arguments"];
  muted?: boolean;
}) {
  return (
    <div>
      <p className="text-[10px] font-semibold uppercase tracking-[0.16em] text-muted-foreground">
        {label}
      </p>
      <ul
        className={cn(
          "mt-1 space-y-1",
          muted ? "text-muted-foreground" : "text-ink",
        )}
      >
        {args.map((a) => (
          <li key={a.name} className="text-xs">
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-[11px] text-ink">
              {a.name}
            </code>
            <span className="ml-2 text-xs text-muted-foreground">
              {a.description}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function ModelClassChip({ modelClass }: { modelClass: McpPrompt["modelClass"] }) {
  const label = modelClass === "reasoning" ? "Reasoning" : "Small";
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-semibold tracking-[0.06em]",
        modelClass === "reasoning"
          ? "bg-accent/10 text-[var(--color-accent)] border-accent/25"
          : "bg-muted text-muted-foreground border-border",
      )}
      aria-label={`Recommended model class: ${label}`}
    >
      <Cpu className="h-3 w-3" aria-hidden="true" />
      {label}
    </span>
  );
}

function SafetyDisclaimerChip() {
  return (
    <span
      className="inline-flex items-center gap-1 rounded-full border border-warning/30 bg-warning/10 px-2 py-0.5 text-[10px] font-semibold tracking-[0.06em] text-warning"
      aria-label="Simulate output before applying to production"
    >
      <AlertTriangle className="h-3 w-3" aria-hidden="true" />
      Simulate first
    </span>
  );
}
