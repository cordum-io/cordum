import { useEffect, useState } from 'react';
import { Card, CardContent } from '../components/ui/card';
import { apiFetch, type Job } from '../lib/api';
import { Search, CheckCircle, XCircle, Clock, AlertCircle, PlayCircle, Hash, Calendar, GitFork, Box, Sparkles, Activity } from 'lucide-react';
import clsx from 'clsx';

const JobExplorer = () => {
    const [jobs, setJobs] = useState<Job[]>([]);
    const [search, setSearch] = useState('');
    const [selectedJob, setSelectedJob] = useState<any | null>(null);
    const [traceJobs, setTraceJobs] = useState<Job[]>([]);

    const fetchJobs = async () => {
        try {
            const res = await apiFetch(`/api/v1/jobs`);
            if (res.ok) {
                const data = await res.json();
                const normalized = (data || []).map((j: any) => ({
                    id: j.id || j.ID,
                    updatedAt: j.updatedAt ?? j.UpdatedAt,
                    state: j.state || j.State,
                    traceId: j.traceId || j.TraceId,
                    resultPtr: j.resultPtr || j.result_ptr,
                }));
                setJobs(normalized);
            }
        } catch (e) {
            console.error(e);
        }
    };

    const fetchJobDetails = async (id: string) => {
        try {
            const res = await apiFetch(`/api/v1/jobs/${id}`);
            if (res.ok) {
                const data = await res.json();
                setSelectedJob({
                    ...data,
                    id: data.id || data.ID,
                    state: data.state || data.State,
                    traceId: data.traceId || data.TraceId,
                    updatedAt: data.updatedAt ?? data.UpdatedAt,
                    resultPtr: data.resultPtr || data.result_ptr,
                });

                // Fetch trace details if traceId is available
                if (data.traceId) { // traceId is camelCase from protojson
                    const traceRes = await apiFetch(`/api/v1/traces/${data.traceId}`);
                    if (traceRes.ok) {
                        const traceData = await traceRes.json();
                        setTraceJobs(traceData || []);
                    }
                } else {
                    setTraceJobs([]);
                }
            }
        } catch (e) {
            console.error(e);
        }
    };

    useEffect(() => {
        fetchJobs();
        const interval = setInterval(fetchJobs, 5000);
        return () => clearInterval(interval);
    }, []);

    const filteredJobs = (jobs || []).filter(j => j?.id?.toLowerCase().includes((search || '').toLowerCase()));

    const getStatusBadge = (state: string) => {
        const s = (state || '').replace('JobState', '').toUpperCase();
        let color = "text-slate-400 bg-slate-800 border-slate-700";
        let Icon = AlertCircle;

        if (s === 'COMPLETED' || s === 'SUCCEEDED') {
            color = "text-green-400 bg-green-900/30 border-green-900/50";
            Icon = CheckCircle;
        } else if (s === 'FAILED' || s === 'DENIED') {
            color = "text-red-400 bg-red-900/30 border-red-900/50";
            Icon = XCircle;
        } else if (s === 'RUNNING' || s === 'DISPATCHED') {
            color = "text-blue-400 bg-blue-900/30 border-blue-900/50";
            Icon = PlayCircle;
        } else if (s === 'PENDING') {
             color = "text-yellow-400 bg-yellow-900/30 border-yellow-900/50";
            Icon = Clock;
        }

        return (
            <div className={clsx("flex items-center gap-1.5 px-2 py-0.5 rounded text-[10px] font-bold border", color)}>
                <Icon size={10} />
                {s}
            </div>
        );
    };

    return (
        <div className="p-6 h-[calc(100vh-64px)] flex flex-col">
             <div className="flex justify-between items-center mb-6">
                <div>
                    <h2 className="text-2xl font-bold text-slate-100">Job Explorer</h2>
                    <p className="text-sm text-slate-500">Inspect execution traces</p>
                </div>
                
                 <div className="relative w-96">
                    <Search className="absolute left-3 top-2.5 h-4 w-4 text-slate-500" />
                    <input 
                        type="text" 
                        placeholder="Search by Job ID..." 
                        className="w-full bg-slate-900 border border-slate-800 rounded-md py-2 pl-10 pr-4 text-sm text-slate-200 placeholder:text-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-500 focus:border-indigo-500 transition-all"
                        value={search}
                        onChange={(e) => setSearch(e.target.value)}
                    />
                </div>
            </div>

            <div className="flex-1 grid grid-cols-1 lg:grid-cols-3 gap-6 min-h-0">
                {/* Job List */}
                <Card className="lg:col-span-2 bg-slate-900 border-slate-800 shadow-sm flex flex-col overflow-hidden">
                    <div className="overflow-y-auto flex-1">
                        <table className="w-full text-left text-sm border-collapse">
                            <thead className="bg-slate-950/80 sticky top-0 text-slate-500 backdrop-blur-sm z-10">
                                <tr>
                                    <th className="p-4 font-medium text-xs uppercase tracking-wider w-[140px]">Status</th>
                                    <th className="p-4 font-medium text-xs uppercase tracking-wider">Job ID</th>
                                    <th className="p-4 font-medium text-xs uppercase tracking-wider text-right">Updated</th>
                                </tr>
                            </thead>
                            <tbody className="divide-y divide-slate-800/50">
                                {filteredJobs.map(job => (
                                    <tr 
                                        key={job.id} 
                                        className={clsx(
                                            "hover:bg-indigo-900/10 cursor-pointer transition-colors group",
                                            selectedJob?.id === job.id ? "bg-indigo-900/20" : ""
                                        )}
                                        onClick={() => fetchJobDetails(job.id)}
                                    >
                                        <td className="p-3 pl-4">
                                            {getStatusBadge(job.state)}
                                        </td>
                                        <td className="p-3 font-mono text-slate-300 text-xs group-hover:text-indigo-300 transition-colors">
                                            {job.id}
                                        </td>
                                        <td className="p-3 pr-4 text-slate-500 text-xs text-right font-mono">
                                            {job.updatedAt ? new Date(job.updatedAt * 1000).toLocaleTimeString() : '-'}
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                        {filteredJobs.length === 0 && (
                            <div className="p-8 text-center text-slate-600 text-sm">
                                No jobs found matching your criteria.
                            </div>
                        )}
                    </div>
                </Card>

                {/* Detail View */}
                <Card className="bg-slate-900 border-slate-800 shadow-sm flex flex-col h-full overflow-hidden">
                    <div className="p-4 border-b border-slate-800 bg-slate-950/50">
                         <h3 className="font-bold text-slate-200 text-sm">Trace Details</h3>
                    </div>
                    <CardContent className="flex-1 overflow-y-auto p-0">
                        {selectedJob ? (
                            <div className="p-6 space-y-6">
                                <div>
                                    <label className="flex items-center gap-2 text-[10px] font-bold text-slate-500 uppercase tracking-wider mb-2">
                                        <Hash size={12} /> Job Identifier
                                    </label>
                                    <div className="font-mono text-sm text-indigo-300 bg-indigo-950/30 p-2 rounded border border-indigo-900/50 break-all select-all">
                                        {selectedJob.id}
                                    </div>
                                </div>

                                {selectedJob.traceId && (
                                    <div>
                                        <label className="flex items-center gap-2 text-[10px] font-bold text-slate-500 uppercase tracking-wider mb-2">
                                            <GitFork size={12} /> Workflow Trace
                                        </label>
                                        <div className="font-mono text-sm text-slate-300 bg-slate-950 p-3 rounded border border-slate-800 break-all select-all">
                                            {selectedJob.traceId}
                                        </div>
                                        {traceJobs.length > 0 && (
                                            <div className="mt-4 space-y-2">
                                                {traceJobs.map(tJob => (
                                                    <div key={tJob.id} className="flex items-center gap-2 text-xs">
                                                        {getStatusBadge(tJob.state)}
                                                        <span className="font-mono text-slate-400">{tJob.id.substring(0,8)}...</span>
                                                        <span className="text-slate-600">
                                                            {tJob.updatedAt ? new Date(tJob.updatedAt * 1000).toLocaleTimeString() : '-'}
                                                        </span>
                                                    </div>
                                                ))}
                                            </div>
                                        )}
                                    </div>
                                )}

                                <div>
                                    <label className="flex items-center gap-2 text-[10px] font-bold text-slate-500 uppercase tracking-wider mb-2">
                                        <Activity size={12} /> State
                                    </label>
                                    {getStatusBadge(selectedJob.state)}
                                </div>
                                
                                <div className="grid grid-cols-2 gap-4">
                                    <div>
                                         <label className="flex items-center gap-2 text-[10px] font-bold text-slate-500 uppercase tracking-wider mb-2">
                                            <Calendar size={12} /> Timestamp
                                        </label>
                                        <div className="text-sm font-mono text-slate-300">
                                            {selectedJob.updatedAt ? new Date(selectedJob.updatedAt * 1000).toLocaleTimeString() : '-'}
                                        </div>
                                    </div>
                                </div>

                                <div>
                                    <label className="flex items-center gap-2 text-[10px] font-bold text-slate-500 uppercase tracking-wider mb-2">
                                        <Box size={12} /> Result Pointer
                                    </label>
                                    <div className="font-mono text-xs text-slate-400 bg-slate-950 p-3 rounded border border-slate-800 break-all">
                                        {selectedJob.resultPtr || <span className="text-slate-600 italic">None</span>}
                                    </div>
                                </div>

                                {selectedJob.result && (
                                    <div>
                                        <label className="flex items-center gap-2 text-[10px] font-bold text-slate-500 uppercase tracking-wider mb-2">
                                            <Sparkles size={12} /> Result Payload
                                        </label>
                                        <div className="font-mono text-xs text-slate-400 bg-slate-950 p-3 rounded border border-slate-800 break-all">
                                            <pre className="whitespace-pre-wrap">{JSON.stringify(selectedJob.result, null, 2)}</pre>
                                        </div>
                                    </div>
                                )}
                                
                                <div className="pt-4 border-t border-slate-800">
                                    <div className="text-xs text-slate-500 text-center">
                                        Full trace logs available in Observability Plane
                                    </div>
                                </div>
                            </div>
                        ) : (
                            <div className="h-full flex flex-col items-center justify-center text-slate-600 space-y-3 p-8 text-center">
                                <div className="p-4 bg-slate-800/50 rounded-full">
                                    <Search size={24} className="opacity-50" />
                                </div>
                                <div>
                                    <p className="text-sm font-medium text-slate-400">No Job Selected</p>
                                    <p className="text-xs mt-1">Select a job row to view deep trace inspection.</p>
                                </div>
                            </div>
                        )}
                    </CardContent>
                </Card>
            </div>
        </div>
    );
};

export default JobExplorer;
