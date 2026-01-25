import { useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { ArrowUpRight, Filter, Sparkles } from "lucide-react";
import { api } from "../lib/api";
import { epochToMillis, formatRelative } from "../lib/format";
import { useUiStore } from "../state/ui";
import { Card, CardHeader, CardTitle } from "../components/ui/Card";
import { Input } from "../components/ui/Input";
import { Select } from "../components/ui/Select";
import { Button } from "../components/ui/Button";
import { JobStatusBadge } from "../components/StatusBadge";
import type { JobRecord } from "../types/api";

const stateOptions = [
  "all",
  "PENDING",
  "APPROVAL_REQUIRED",
  "SCHEDULED",
  "DISPATCHED",
  "RUNNING",
  "SUCCEEDED",
  "FAILED",
  "CANCELLED",
  "TIMEOUT",
  "DENIED",
];

function jobUpdatedAt(job: JobRecord) {
  const ms = epochToMillis(job.updated_at);
  if (!ms) {
    return "";
  }
  return new Date(ms).toISOString();
}

export function JobsPage() {
  const navigate = useNavigate();
  const globalSearch = useUiStore((state) => state.globalSearch);

  const [stateFilter, setStateFilter] = useState("all");
  const [topicFilter, setTopicFilter] = useState("");
  const [tenantFilter, setTenantFilter] = useState("");
  const [teamFilter, setTeamFilter] = useState("");
  const [traceFilter, setTraceFilter] = useState("");
  const [searchQuery, setSearchQuery] = useState("");

  const serverParams = useMemo(() => {
    const params: {
      limit?: number;
      state?: string;
      topic?: string;
      tenant?: string;
      team?: string;
      trace_id?: string;
    } = { limit: 100 };
    if (stateFilter !== "all") {
      params.state = stateFilter;
    }
    if (topicFilter.trim()) {
      params.topic = topicFilter.trim();
    }
    if (tenantFilter.trim()) {
      params.tenant = tenantFilter.trim();
    }
    if (teamFilter.trim()) {
      params.team = teamFilter.trim();
    }
    if (traceFilter.trim()) {
      params.trace_id = traceFilter.trim();
    }
    return params;
  }, [stateFilter, topicFilter, tenantFilter, teamFilter, traceFilter]);

  const jobsQuery = useInfiniteQuery({
    queryKey: ["jobs", serverParams],
    queryFn: ({ pageParam }) => api.listJobs({ ...serverParams, cursor: pageParam as number | undefined }),
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    initialPageParam: undefined as number | undefined,
  });

  const jobs = jobsQuery.data?.pages.flatMap((page) => page.items) ?? [];
  const activeCount = useMemo(
    () => jobs.filter((job) => ["PENDING", "SCHEDULED", "DISPATCHED", "RUNNING"].includes(job.state)).length,
    [jobs]
  );
  const approvalCount = useMemo(() => jobs.filter((job) => job.state === "APPROVAL_REQUIRED").length, [jobs]);
  const failedCount = useMemo(
    () => jobs.filter((job) => ["FAILED", "DENIED", "TIMEOUT", "CANCELLED"].includes(job.state)).length,
    [jobs]
  );
  const runningCount = useMemo(() => jobs.filter((job) => job.state === "RUNNING").length, [jobs]);
  const failedOnlyCount = useMemo(() => jobs.filter((job) => job.state === "FAILED").length, [jobs]);
  const hasFilters =
    stateFilter !== "all" ||
    topicFilter.trim().length > 0 ||
    tenantFilter.trim().length > 0 ||
    teamFilter.trim().length > 0 ||
    traceFilter.trim().length > 0 ||
    searchQuery.trim().length > 0;
  const filteredJobs = useMemo(() => {
    const query = (searchQuery || globalSearch).toLowerCase();
    if (!query) {
      return jobs;
    }
    return jobs.filter((job) => {
      const fields = [
        job.id,
        job.topic,
        job.tenant,
        job.team,
        job.pack_id,
        job.capability,
        job.trace_id,
      ]
        .filter(Boolean)
        .map((value) => String(value).toLowerCase());
      return fields.some((value) => value.includes(query));
    });
  }, [jobs, searchQuery, globalSearch]);
  const activeFilters = useMemo(() => {
    const filters: string[] = [];
    if (stateFilter !== "all") {
      filters.push(`State: ${stateFilter}`);
    }
    if (topicFilter.trim()) {
      filters.push(`Topic: ${topicFilter.trim()}`);
    }
    if (tenantFilter.trim()) {
      filters.push(`Tenant: ${tenantFilter.trim()}`);
    }
    if (teamFilter.trim()) {
      filters.push(`Team: ${teamFilter.trim()}`);
    }
    if (traceFilter.trim()) {
      filters.push(`Trace: ${traceFilter.trim()}`);
    }
    if (searchQuery.trim()) {
      filters.push(`Search: ${searchQuery.trim()}`);
    }
    return filters;
  }, [stateFilter, topicFilter, tenantFilter, teamFilter, traceFilter, searchQuery]);

  const resetFilters = () => {
    setStateFilter("all");
    setTopicFilter("");
    setTenantFilter("");
    setTeamFilter("");
    setTraceFilter("");
    setSearchQuery("");
  };

  return (
    <div className="space-y-6">
      <section className="relative overflow-hidden rounded-3xl border border-border bg-[color:var(--surface-glass)] p-6 lg:p-8">
        <div className="pointer-events-none absolute -right-10 top-0 h-48 w-48 rounded-full bg-[color:rgba(15,127,122,0.2)] blur-3xl" />
        <div className="pointer-events-none absolute -left-16 bottom-0 h-48 w-48 rounded-full bg-[color:rgba(212,131,58,0.18)] blur-3xl" />
        <div className="relative grid gap-6 lg:grid-cols-[1.6fr_0.2fr_1fr]">
          <div>
            <div className="inline-flex items-center gap-2 rounded-full border border-border bg-white/80 px-3 py-1 text-[10px] font-semibold uppercase tracking-[0.2em] text-muted">
              <Sparkles className="h-3 w-3 text-accent" />
              Jobs
            </div>
            <h2 className="mt-4 font-display text-3xl font-semibold text-ink">Track work as it moves through Cordum.</h2>
            <p className="mt-2 text-sm text-muted">Jump straight to approvals, active jobs, or recent failures.</p>
            <div className="mt-5 flex flex-wrap gap-3">
              <Button variant="primary" type="button" onClick={() => navigate("/trace")}>
                Open trace
              </Button>
              <Button variant="outline" type="button" onClick={() => navigate("/policy")}>
                Review approvals
              </Button>
              <Button variant="ghost" type="button" onClick={() => navigate("/runs")}>
                View runs
              </Button>
            </div>
            <div className="mt-6 flex flex-wrap gap-3 text-xs text-muted">
              <div className="rounded-full border border-border bg-white/80 px-3 py-1">
                <span className="font-semibold text-ink">{filteredJobs.length}</span> shown
              </div>
              <div className="rounded-full border border-border bg-white/80 px-3 py-1">
                <span className="font-semibold text-ink">{activeCount}</span> active
              </div>
              <div className="rounded-full border border-border bg-white/80 px-3 py-1">
                <span className="font-semibold text-ink">{approvalCount}</span> approvals
              </div>
              <div className="rounded-full border border-border bg-white/80 px-3 py-1">
                <span className="font-semibold text-ink">{failedCount}</span> failed
              </div>
            </div>
          </div>
          <div className="hidden lg:block" />
          <div className="space-y-4">
            <div className="rounded-2xl border border-border bg-white/70 p-4">
              <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Quick actions</div>
              <div className="mt-3 space-y-2">
                <button
                  type="button"
                  onClick={() => {
                    setStateFilter("APPROVAL_REQUIRED");
                    setSearchQuery("");
                  }}
                  className="flex w-full items-center justify-between rounded-xl border border-border bg-white/80 px-3 py-2 text-left transition hover:border-accent"
                >
                  <div>
                    <div className="font-semibold text-ink">Approvals queue</div>
                    <div className="text-xs text-muted">{approvalCount} waiting</div>
                  </div>
                  <ArrowUpRight className="h-4 w-4 text-muted" />
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setStateFilter("RUNNING");
                    setSearchQuery("");
                  }}
                  className="flex w-full items-center justify-between rounded-xl border border-border bg-white/80 px-3 py-2 text-left transition hover:border-accent"
                >
                  <div>
                    <div className="font-semibold text-ink">Running now</div>
                    <div className="text-xs text-muted">{runningCount} jobs</div>
                  </div>
                  <ArrowUpRight className="h-4 w-4 text-muted" />
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setStateFilter("FAILED");
                    setSearchQuery("");
                  }}
                  className="flex w-full items-center justify-between rounded-xl border border-border bg-white/80 px-3 py-2 text-left transition hover:border-accent"
                >
                  <div>
                    <div className="font-semibold text-ink">Failed jobs</div>
                    <div className="text-xs text-muted">{failedOnlyCount} jobs</div>
                  </div>
                  <ArrowUpRight className="h-4 w-4 text-muted" />
                </button>
              </div>
            </div>
            <div className="rounded-2xl border border-border bg-white/70 p-4">
              <div className="flex items-center justify-between text-xs font-semibold uppercase tracking-[0.2em] text-muted">
                Active filters
                <Filter className="h-4 w-4 text-muted" />
              </div>
              {activeFilters.length ? (
                <div className="mt-3 flex flex-wrap gap-2 text-xs">
                  {activeFilters.map((filter) => (
                    <span key={filter} className="rounded-full border border-border bg-white/80 px-3 py-1 text-ink">
                      {filter}
                    </span>
                  ))}
                </div>
              ) : (
                <div className="mt-3 text-sm text-muted">No filters applied. Showing most recent jobs.</div>
              )}
            </div>
          </div>
        </div>
      </section>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>Jobs</CardTitle>
            <div className="text-xs text-muted">Narrow the list to the jobs you need to action.</div>
          </div>
          {hasFilters ? (
            <Button variant="outline" size="sm" type="button" onClick={resetFilters}>
              Clear filters
            </Button>
          ) : null}
        </CardHeader>
        <div className="grid gap-3 lg:grid-cols-6">
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">State</label>
            <Select value={stateFilter} onChange={(event) => setStateFilter(event.target.value)}>
              {stateOptions.map((state) => (
                <option key={state} value={state}>
                  {state === "all" ? "Any" : state}
                </option>
              ))}
            </Select>
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Topic</label>
            <Input value={topicFilter} onChange={(event) => setTopicFilter(event.target.value)} placeholder="job.*" />
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Tenant</label>
            <Input value={tenantFilter} onChange={(event) => setTenantFilter(event.target.value)} placeholder="default" />
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Team</label>
            <Input value={teamFilter} onChange={(event) => setTeamFilter(event.target.value)} placeholder="team" />
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Trace</label>
            <Input value={traceFilter} onChange={(event) => setTraceFilter(event.target.value)} placeholder="trace id" />
          </div>
          <div>
            <label className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Search</label>
            <Input value={searchQuery} onChange={(event) => setSearchQuery(event.target.value)} placeholder="job id" />
          </div>
        </div>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Job List</CardTitle>
          <div className="text-xs text-muted">
            Showing {filteredJobs.length} of {jobs.length}
          </div>
        </CardHeader>
        {jobsQuery.isLoading ? (
          <div className="text-sm text-muted">Loading jobs...</div>
        ) : filteredJobs.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">No jobs found.</div>
        ) : (
          <div className="space-y-3">
            {filteredJobs.map((job) => (
              <div key={job.id} className="list-row">
                <div className="grid gap-3 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)_auto] lg:items-center">
                  <div>
                    <Link
                      to={`/jobs/${job.id}`}
                      className="text-sm font-semibold text-ink hover:underline"
                    >
                      Job {job.id.slice(0, 10)}
                    </Link>
                    <div className="text-xs text-muted">Topic {job.topic || "-"}</div>
                    {job.risk_tags && job.risk_tags.length > 0 ? (
                      <div className="mt-1 flex flex-wrap gap-1">
                        {job.risk_tags.map((tag) => (
                          <span
                            key={tag}
                            className="rounded bg-danger/10 px-1.5 py-0.5 text-[10px] font-medium text-danger"
                          >
                            {tag}
                          </span>
                        ))}
                      </div>
                    ) : null}
                  </div>
                  <div className="text-xs text-muted">
                    <div>Tenant {job.tenant || "default"}</div>
                    <div>Pack {job.pack_id || "-"}</div>
                    {job.capability ? <div>Cap: {job.capability}</div> : null}
                  </div>
                  <div className="flex flex-col items-end gap-1">
                    <JobStatusBadge state={job.state} />
                    <div className="text-xs text-muted">{formatRelative(jobUpdatedAt(job))}</div>
                  </div>
                </div>
                <div className="mt-2 flex flex-wrap gap-2">
                  <Link to={`/jobs/${job.id}`}>
                    <Button variant="outline" size="sm" type="button">
                      Details
                    </Button>
                  </Link>
                  <Link to={`/jobs/${job.id}?tab=safety`}>
                    <Button variant="outline" size="sm" type="button">
                      Decisions
                    </Button>
                  </Link>
                  {job.run_id ? (
                    <Link to={`/runs/${job.run_id}`}>
                      <Button variant="outline" size="sm" type="button">
                        Run Timeline
                      </Button>
                    </Link>
                  ) : null}
                  {job.trace_id ? (
                    <Link to={`/trace/${job.trace_id}`}>
                      <Button variant="outline" size="sm" type="button">
                        Trace
                      </Button>
                    </Link>
                  ) : null}
                </div>
              </div>
            ))}
          </div>
        )}
        {jobsQuery.hasNextPage ? (
          <div className="mt-4">
            <Button
              variant="outline"
              size="sm"
              type="button"
              onClick={() => jobsQuery.fetchNextPage()}
              disabled={jobsQuery.isFetchingNextPage}
            >
              {jobsQuery.isFetchingNextPage ? "Loading..." : "Load more"}
            </Button>
          </div>
        ) : null}
      </Card>
    </div>
  );
}
