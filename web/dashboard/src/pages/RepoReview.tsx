import { useEffect, useMemo, useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "../components/ui/card";
import { apiFetch } from "../lib/api";
import { CheckCircle, Clock, GitBranch, Loader2, Play, XCircle } from "lucide-react";
import clsx from "clsx";

type RepoReviewResponse = { job_id: string; trace_id: string };

type JobDetail = {
  id: string;
  state: string;
  result_ptr?: string;
  result?: any;
  traceId?: string;
};

const defaultInclude = ["**/*.go", "**/*.ts", "**/*.tsx", "**/*.js", "**/*.jsx", "**/*.py"].join("\n");
const defaultExclude = ["vendor/**", "node_modules/**", "dist/**", ".git/**", "build/**"].join("\n");

const statusBadge = (state?: string) => {
  const s = (state || "").toUpperCase();
  let color = "bg-slate-800 text-slate-300 border-slate-700";
  let Icon: any = Clock;
  if (s.includes("SUCCEEDED") || s.includes("COMPLETED")) {
    color = "bg-green-900/30 text-green-300 border-green-800";
    Icon = CheckCircle;
  } else if (s.includes("FAILED") || s.includes("DENIED")) {
    color = "bg-red-900/30 text-red-300 border-red-800";
    Icon = XCircle;
  } else if (s.includes("RUNNING") || s.includes("DISPATCHED")) {
    color = "bg-blue-900/30 text-blue-300 border-blue-800";
    Icon = Play;
  } else if (s.includes("PENDING")) {
    color = "bg-yellow-900/30 text-yellow-300 border-yellow-800";
    Icon = Clock;
  }
  return (
    <span className={clsx("inline-flex items-center gap-1 px-2 py-1 rounded text-[10px] font-bold border", color)}>
      <Icon size={12} /> {s || "UNKNOWN"}
    </span>
  );
};

const RepoReview = () => {
  const [repoUrl, setRepoUrl] = useState("");
  const [branch, setBranch] = useState("main");
  const [localPath, setLocalPath] = useState("");
  const [includeGlobs, setIncludeGlobs] = useState(defaultInclude);
  const [excludeGlobs, setExcludeGlobs] = useState(defaultExclude);
  const [maxFiles, setMaxFiles] = useState(200);
  const [batchSize, setBatchSize] = useState(40);
  const [maxBatches, setMaxBatches] = useState(3);
  const [runTests, setRunTests] = useState(false);
  const [testCommand, setTestCommand] = useState("go test ./...");
  const [priority, setPriority] = useState("interactive");

  const [job, setJob] = useState<JobDetail | null>(null);
  const [traceJobs, setTraceJobs] = useState<any[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const hasReport = useMemo(() => Boolean(job?.result?.summary), [job]);

  const parseList = (text: string) =>
    text
      .split(/\n|,/)
      .map((t) => t.trim())
      .filter(Boolean);

  const submit = async () => {
    setSubmitting(true);
    setError(null);
    try {
      const payload = {
        repo_url: repoUrl,
        branch,
        local_path: localPath,
        include_globs: parseList(includeGlobs),
        exclude_globs: parseList(excludeGlobs),
        max_files: maxFiles,
        batch_size: batchSize,
        max_batches: maxBatches,
        run_tests: runTests,
        test_command: testCommand,
        priority,
      };
      const res = await apiFetch("/api/v1/repo-review", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      if (!res.ok) {
        const msg = await res.text();
        throw new Error(msg || "submission failed");
      }
      const data: RepoReviewResponse = await res.json();
      setJob({ id: data.job_id, state: "PENDING", traceId: data.trace_id });
    } catch (e: any) {
      setError(e?.message || "submission failed");
    } finally {
      setSubmitting(false);
    }
  };

  const fetchJob = async (id: string, traceId?: string) => {
    try {
      const res = await apiFetch(`/api/v1/jobs/${id}`);
      if (!res.ok) return;
      const data = await res.json();
      setJob({
        id: data.id || data.ID,
        state: data.state || data.State,
        result_ptr: data.result_ptr || data.resultPtr,
        result: data.result,
        traceId: data.traceId || data.trace_id || traceId,
      });
      const t = data.traceId || data.trace_id || traceId;
      if (t) {
        const traceRes = await apiFetch(`/api/v1/traces/${t}`);
        if (traceRes.ok) {
          const tData = await traceRes.json();
          setTraceJobs(tData || []);
        }
      }
    } catch (e) {
      console.error(e);
    }
  };

  useEffect(() => {
    if (!job?.id) return;
    fetchJob(job.id, job.traceId);
    const interval = setInterval(() => fetchJob(job.id, job.traceId), 4000);
    return () => clearInterval(interval);
  }, [job?.id]);

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <div className="flex items-center gap-2 text-slate-500 text-xs uppercase tracking-widest">
            <GitBranch size={14} /> Repo Review Workflow
          </div>
          <h2 className="text-2xl font-bold text-slate-100">Run full-repo code review</h2>
          <p className="text-sm text-slate-500">Scans, partitions, reviews files, optional tests, and emits a report.</p>
        </div>
        {job?.id && (
          <div className="text-right">
            <div className="text-xs text-slate-500 mb-1">Job ID</div>
            <div className="font-mono text-sm text-indigo-300 bg-indigo-950/40 px-3 py-2 rounded border border-indigo-900/50">
              {job.id.slice(0, 12)}...
            </div>
          </div>
        )}
      </div>

      <div className="grid grid-cols-1 xl:grid-cols-2 gap-6">
        <Card className="bg-slate-900 border-slate-800 shadow-lg shadow-indigo-500/5">
          <CardHeader className="border-b border-slate-800">
            <CardTitle className="text-sm text-slate-200">Workflow Inputs</CardTitle>
          </CardHeader>
          <CardContent className="p-6 space-y-4">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label className="text-xs text-slate-400">Repo URL</label>
                <input
                  value={repoUrl}
                  onChange={(e) => setRepoUrl(e.target.value)}
                  placeholder="git@github.com:org/repo.git"
                  className="w-full bg-slate-950 border border-slate-800 rounded px-3 py-2 text-sm text-slate-100 focus:ring-1 focus:ring-indigo-500"
                />
                <p className="text-[11px] text-slate-500 mt-1">Leave blank to use local path</p>
              </div>
              <div>
                <label className="text-xs text-slate-400">Branch</label>
                <input
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                  className="w-full bg-slate-950 border border-slate-800 rounded px-3 py-2 text-sm text-slate-100 focus:ring-1 focus:ring-indigo-500"
                />
              </div>
              <div>
                <label className="text-xs text-slate-400">Local Path (optional)</label>
                <input
                  value={localPath}
                  onChange={(e) => setLocalPath(e.target.value)}
                  placeholder="/repos/project"
                  className="w-full bg-slate-950 border border-slate-800 rounded px-3 py-2 text-sm text-slate-100 focus:ring-1 focus:ring-indigo-500"
                />
              </div>
              <div className="grid grid-cols-3 gap-3">
                <div>
                  <label className="text-xs text-slate-400">Max Files</label>
                  <input
                    type="number"
                    value={maxFiles}
                    onChange={(e) => setMaxFiles(parseInt(e.target.value, 10) || 0)}
                    className="w-full bg-slate-950 border border-slate-800 rounded px-3 py-2 text-sm text-slate-100 focus:ring-1 focus:ring-indigo-500"
                  />
                </div>
                <div>
                  <label className="text-xs text-slate-400">Batch Size</label>
                  <input
                    type="number"
                    value={batchSize}
                    onChange={(e) => setBatchSize(parseInt(e.target.value, 10) || 0)}
                    className="w-full bg-slate-950 border border-slate-800 rounded px-3 py-2 text-sm text-slate-100 focus:ring-1 focus:ring-indigo-500"
                  />
                </div>
                <div>
                  <label className="text-xs text-slate-400">Max Batches</label>
                  <input
                    type="number"
                    value={maxBatches}
                    onChange={(e) => setMaxBatches(parseInt(e.target.value, 10) || 0)}
                    className="w-full bg-slate-950 border border-slate-800 rounded px-3 py-2 text-sm text-slate-100 focus:ring-1 focus:ring-indigo-500"
                  />
                </div>
              </div>
              <div>
                <label className="text-xs text-slate-400">Priority</label>
                <select
                  value={priority}
                  onChange={(e) => setPriority(e.target.value)}
                  className="w-full bg-slate-950 border border-slate-800 rounded px-3 py-2 text-sm text-slate-100 focus:ring-1 focus:ring-indigo-500"
                >
                  <option value="interactive">Interactive</option>
                  <option value="batch">Batch</option>
                  <option value="critical">Critical</option>
                </select>
              </div>
              <div>
                <label className="text-xs text-slate-400 flex items-center gap-2">
                  Include Globs <span className="text-[10px] text-slate-500">(one per line)</span>
                </label>
                <textarea
                  value={includeGlobs}
                  onChange={(e) => setIncludeGlobs(e.target.value)}
                  className="w-full bg-slate-950 border border-slate-800 rounded px-3 py-2 text-sm text-slate-100 h-24 focus:ring-1 focus:ring-indigo-500"
                />
              </div>
              <div>
                <label className="text-xs text-slate-400 flex items-center gap-2">
                  Exclude Globs <span className="text-[10px] text-slate-500">(one per line)</span>
                </label>
                <textarea
                  value={excludeGlobs}
                  onChange={(e) => setExcludeGlobs(e.target.value)}
                  className="w-full bg-slate-950 border border-slate-800 rounded px-3 py-2 text-sm text-slate-100 h-24 focus:ring-1 focus:ring-indigo-500"
                />
              </div>
              <div>
                <label className="text-xs text-slate-400">Test Command</label>
                <input
                  value={testCommand}
                  onChange={(e) => setTestCommand(e.target.value)}
                  className="w-full bg-slate-950 border border-slate-800 rounded px-3 py-2 text-sm text-slate-100 focus:ring-1 focus:ring-indigo-500"
                  disabled={!runTests}
                />
                <div className="flex items-center gap-2 mt-2 text-sm">
                  <input
                    type="checkbox"
                    checked={runTests}
                    onChange={(e) => setRunTests(e.target.checked)}
                    className="accent-indigo-500"
                  />
                  <span className="text-slate-400 text-sm">Run tests after code review</span>
                </div>
              </div>
            </div>

            {error && <div className="text-sm text-red-400 bg-red-900/20 border border-red-900/50 rounded px-3 py-2">{error}</div>}

            <button
              onClick={submit}
              disabled={submitting}
              className="px-4 py-2 bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-semibold rounded shadow shadow-indigo-500/20 flex items-center gap-2"
            >
              {submitting && <Loader2 size={16} className="animate-spin" />}
              Launch Review
            </button>
          </CardContent>
        </Card>

        <div className="space-y-4">
          <Card className="bg-slate-900 border-slate-800 shadow-sm">
            <CardHeader className="border-b border-slate-800">
              <CardTitle className="text-sm text-slate-200">Workflow Status</CardTitle>
            </CardHeader>
            <CardContent className="p-6 space-y-3">
              <div className="flex items-center justify-between">
                <div className="text-sm text-slate-400">State</div>
                {statusBadge(job?.state)}
              </div>
              {job?.traceId && (
                <div className="text-xs text-slate-500">
                  Trace: <span className="font-mono text-indigo-300">{job.traceId}</span>
                </div>
              )}
              <div className="mt-3">
                <div className="text-[11px] text-slate-500 uppercase font-bold mb-2">Trace Jobs</div>
                <div className="space-y-1 max-h-40 overflow-auto">
                  {traceJobs.length === 0 && <div className="text-slate-600 text-sm">No trace events yet.</div>}
                  {traceJobs.map((tj) => (
                    <div key={tj.ID || tj.id} className="flex items-center justify-between text-xs bg-slate-950/60 border border-slate-800 rounded px-2 py-1">
                      <span className="font-mono text-slate-300">{(tj.ID || tj.id || "").slice(0, 10)}...</span>
                      {statusBadge(tj.State || tj.state)}
                    </div>
                  ))}
                </div>
              </div>
            </CardContent>
          </Card>

          <Card className="bg-slate-900 border-slate-800 shadow-sm">
            <CardHeader className="border-b border-slate-800">
              <CardTitle className="text-sm text-slate-200">Report</CardTitle>
            </CardHeader>
            <CardContent className="p-6 space-y-3">
              {!hasReport && <div className="text-sm text-slate-500">Awaiting report...</div>}
              {hasReport && (
                <div className="space-y-3">
                  <div className="text-sm text-slate-200 font-semibold">{job?.result?.summary}</div>
                  {(job?.result?.sections || []).map((sec: any, idx: number) => (
                    <div key={idx} className="border border-slate-800 rounded p-3 bg-slate-950/40">
                      <div className="text-xs text-slate-400 uppercase font-bold mb-2">{sec.title}</div>
                      <div className="space-y-2">
                        {(sec.items || []).map((item: any, i: number) => (
                          <div key={i} className="p-2 bg-slate-900 rounded border border-slate-800">
                            <div className="flex items-center justify-between text-sm">
                              <span className="font-mono text-indigo-300">{item.file_path || item.filePath}</span>
                              <span className="text-[10px] uppercase text-slate-500">{item.severity || "info"}</span>
                            </div>
                            <div className="text-sm text-slate-200 mt-1">{item.description}</div>
                          </div>
                        ))}
                      </div>
                    </div>
                  ))}
                  {job?.result?.tests_summary && (
                    <div className="p-3 bg-slate-900 rounded border border-slate-800">
                      <div className="text-xs text-slate-400 uppercase font-bold mb-1">Tests</div>
                      <div className="text-sm text-slate-200">{job.result.tests_summary.details}</div>
                    </div>
                  )}
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
};

export default RepoReview;
