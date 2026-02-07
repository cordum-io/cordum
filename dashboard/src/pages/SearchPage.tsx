import { useState, useEffect, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { Search, Loader } from "lucide-react";
import { Input } from "../components/ui/Input";
import { Card } from "../components/ui/Card";
import { Badge } from "../components/ui/Badge";
import { useJobs } from "../hooks/useJobs";
import { useWorkflows } from "../hooks/useWorkflows";
import { usePacks } from "../hooks/usePacks";

// ---------------------------------------------------------------------------
// Debounce hook
// ---------------------------------------------------------------------------

function useDebouncedValue(value: string, delayMs: number): string {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(id);
  }, [value, delayMs]);
  return debounced;
}

// ---------------------------------------------------------------------------
// Result row
// ---------------------------------------------------------------------------

function ResultRow({
  label,
  detail,
  badge,
  badgeVariant,
  onClick,
}: {
  label: string;
  detail?: string;
  badge?: string;
  badgeVariant?: "default" | "success" | "warning" | "danger" | "info";
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className="flex w-full items-center justify-between rounded-xl px-3 py-2.5 text-left transition-colors hover:bg-surface2/60"
      onClick={onClick}
    >
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium text-ink">{label}</p>
        {detail && (
          <p className="truncate text-xs text-muted">{detail}</p>
        )}
      </div>
      {badge && (
        <Badge variant={badgeVariant ?? "default"} className="ml-2 shrink-0">
          {badge}
        </Badge>
      )}
    </button>
  );
}

// ---------------------------------------------------------------------------
// Section
// ---------------------------------------------------------------------------

function Section({
  title,
  count,
  children,
}: {
  title: string;
  count: number;
  children: React.ReactNode;
}) {
  if (count === 0) return null;
  return (
    <div>
      <h3 className="mb-1 text-xs font-semibold uppercase tracking-wider text-muted">
        {title}{" "}
        <span className="font-mono text-[10px]">({count})</span>
      </h3>
      <Card className="divide-y divide-border">{children}</Card>
    </div>
  );
}

// ---------------------------------------------------------------------------
// SearchPage
// ---------------------------------------------------------------------------

export default function SearchPage() {
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const debouncedQuery = useDebouncedValue(query, 400);
  const lowerQuery = debouncedQuery.toLowerCase();

  // Fetch data
  const { data: jobsData, isLoading: jobsLoading } = useJobs({
    limit: 50,
    topic: debouncedQuery || undefined,
  });
  const { data: workflows, isLoading: wfLoading } = useWorkflows();
  const { data: packsData, isLoading: packsLoading } = usePacks();

  const isSearching = !!debouncedQuery;
  const isLoading = isSearching && (jobsLoading || wfLoading || packsLoading);

  // Filter results client-side
  const jobs = useMemo(() => {
    if (!isSearching) return [];
    return (jobsData?.items ?? []).filter(
      (j) =>
        j.id.toLowerCase().includes(lowerQuery) ||
        (j.topic ?? "").toLowerCase().includes(lowerQuery),
    );
  }, [jobsData, lowerQuery, isSearching]);

  const filteredWorkflows = useMemo(() => {
    if (!isSearching) return [];
    return (workflows ?? []).filter((w) =>
      w.name.toLowerCase().includes(lowerQuery),
    );
  }, [workflows, lowerQuery, isSearching]);

  const packs = useMemo(() => {
    if (!isSearching) return [];
    return (packsData?.items ?? []).filter((p) =>
      p.name.toLowerCase().includes(lowerQuery),
    );
  }, [packsData, lowerQuery, isSearching]);

  const totalResults = jobs.length + filteredWorkflows.length + packs.length;
  const noResults = isSearching && !isLoading && totalResults === 0;

  return (
    <div className="space-y-6">
      <h1 className="font-display text-2xl font-bold text-ink">Search</h1>

      {/* Search input */}
      <div className="relative">
        <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" />
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search jobs, workflows, packs..."
          className="pl-10"
          autoFocus
        />
        {isLoading && (
          <Loader className="absolute right-3 top-1/2 h-4 w-4 -translate-y-1/2 animate-spin text-muted" />
        )}
      </div>

      {/* Empty prompt */}
      {!isSearching && (
        <p className="py-12 text-center text-sm text-muted">
          Start typing to search across jobs, workflows, and packs.
        </p>
      )}

      {/* No results */}
      {noResults && (
        <p className="py-12 text-center text-sm text-muted">
          No results for &ldquo;{debouncedQuery}&rdquo;
        </p>
      )}

      {/* Results */}
      {isSearching && !isLoading && totalResults > 0 && (
        <div className="space-y-4">
          <Section title="Jobs" count={jobs.length}>
            {jobs.slice(0, 10).map((job) => (
              <ResultRow
                key={job.id}
                label={job.id.slice(0, 12)}
                detail={job.topic ?? undefined}
                badge={job.status}
                badgeVariant={
                  job.status === "succeeded"
                    ? "success"
                    : job.status === "failed"
                      ? "danger"
                      : job.status === "running"
                        ? "info"
                        : "default"
                }
                onClick={() => navigate(`/jobs/${job.id}`)}
              />
            ))}
          </Section>

          <Section title="Workflows" count={filteredWorkflows.length}>
            {filteredWorkflows.slice(0, 10).map((wf) => (
              <ResultRow
                key={wf.id}
                label={wf.name}
                detail={wf.description ?? wf.id}
                badge="workflow"
                badgeVariant="info"
                onClick={() => navigate(`/workflows/${wf.id}`)}
              />
            ))}
          </Section>

          <Section title="Packs" count={packs.length}>
            {packs.slice(0, 10).map((pack) => (
              <ResultRow
                key={pack.id}
                label={pack.name}
                detail={`v${pack.version} — ${pack.status}`}
                badge={pack.status}
                badgeVariant={pack.status === "active" ? "success" : "default"}
                onClick={() => navigate("/packs")}
              />
            ))}
          </Section>
        </div>
      )}
    </div>
  );
}
