import { useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
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

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Jobs</CardTitle>
          <div className="text-xs text-muted">Trace live job flow and approvals</div>
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
          <div className="text-xs text-muted">Showing {filteredJobs.length} jobs</div>
        </CardHeader>
        {jobsQuery.isLoading ? (
          <div className="text-sm text-muted">Loading jobs...</div>
        ) : filteredJobs.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-border p-6 text-sm text-muted">No jobs found.</div>
        ) : (
          <div className="space-y-3">
            {filteredJobs.map((job) => (
              <button
                key={job.id}
                type="button"
                onClick={() => navigate(`/jobs/${job.id}`)}
                className="list-row text-left"
              >
                <div className="grid gap-3 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)_auto] lg:items-center">
                  <div>
                    <div className="text-sm font-semibold text-ink">Job {job.id.slice(0, 10)}</div>
                    <div className="text-xs text-muted">Topic {job.topic || "-"}</div>
                  </div>
                  <div className="text-xs text-muted">
                    Tenant {job.tenant || "default"} · Pack {job.pack_id || "-"}
                  </div>
                  <div className="flex flex-col items-end gap-1">
                    <JobStatusBadge state={job.state} />
                    <div className="text-xs text-muted">{formatRelative(jobUpdatedAt(job))}</div>
                  </div>
                </div>
              </button>
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
