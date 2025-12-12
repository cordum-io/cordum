import { useEffect, useMemo, useState } from 'react';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import {
    Activity,
    Zap,
    Terminal,
    History,
    RefreshCw,
    AlertTriangle,
    CheckCircle2,
    Radio,
    Loader2,
    Gauge,
    Clock3,
    Server
} from 'lucide-react';
import { makeWsUrl, apiFetch, type BusPacket } from '../lib/api';
import clsx from 'clsx';

type JobRecord = {
    id: string;
    state: string;
    updatedAt?: number;
    traceId?: string;
    safetyDecision?: string;
    safetyReason?: string;
    topic?: string;
    tenant?: string;
};

type WorkerStatus = {
    id: string;
    pool?: string;
    activeJobs?: number;
    maxParallelJobs?: number;
    capabilities?: string[];
    lastSeen: number;
};

const statusColors: Record<string, string> = {
    COMPLETED: 'text-emerald-300 bg-emerald-500/10 border border-emerald-500/30',
    FAILED: 'text-rose-300 bg-rose-500/10 border border-rose-500/30',
    RUNNING: 'text-amber-300 bg-amber-500/10 border border-amber-500/30',
    PENDING: 'text-sky-300 bg-sky-500/10 border border-sky-500/30',
    UNKNOWN: 'text-slate-300 bg-slate-600/20 border border-slate-700',
};

const StatCard = ({ title, value, sub, icon: Icon, colorClass }: any) => (
  <Card className="bg-slate-900 border-slate-800 shadow-sm">
    <CardContent className="p-4">
      <div className="flex justify-between items-start">
        <div>
          <p className="text-xs font-medium text-slate-500 uppercase tracking-wider">{title}</p>
          <div className="text-2xl font-bold text-slate-100 mt-1 font-mono">{value}</div>
        </div>
        <div className={clsx("p-2 rounded-md bg-opacity-10", colorClass)}>
            <Icon size={18} />
        </div>
      </div>
      <div className="mt-3 text-xs text-slate-400 flex items-center gap-1">
        {sub}
      </div>
    </CardContent>
  </Card>
);

const MissionControl = () => {
    const [events, setEvents] = useState<BusPacket[]>([]);
    const [jobHistory, setJobHistory] = useState<JobRecord[]>([]);
    const [historyLoading, setHistoryLoading] = useState(false);
    const [historyError, setHistoryError] = useState<string | null>(null);
    const [canceling, setCanceling] = useState<Record<string, boolean>>({});
    const [stats, setStats] = useState({
        activeJobs: 0,
        completedJobs: 0,
        eventsCount: 0
    });
    const [connectionStatus, setConnectionStatus] = useState<'connecting' | 'connected' | 'disconnected'>('connecting');
    const [workers, setWorkers] = useState<Record<string, WorkerStatus>>({});
    const [stateFilter, setStateFilter] = useState<'ALL' | 'RUNNING' | 'COMPLETED' | 'FAILED' | 'PENDING'>('ALL');

    useEffect(() => {
        let closed = false;
        let retry = 0;
        let ws: WebSocket | null = null;

        const upsertJob = (jobId: string, state: string, ts: number) => {
            if (!jobId) return;
            setJobHistory(prev => {
                const next = [...prev];
                const idx = next.findIndex(j => j.id === jobId);
                if (idx >= 0) {
                    if (!next[idx].updatedAt || ts >= (next[idx].updatedAt ?? 0)) {
                        next[idx] = { ...next[idx], state, updatedAt: ts };
                    }
                } else {
                    next.push({ id: jobId, state, updatedAt: ts });
                }
                return next
                    .sort((a, b) => (b.updatedAt ?? 0) - (a.updatedAt ?? 0))
                    .slice(0, 80);
            });
        };

        const connect = () => {
            setConnectionStatus('connecting');
            ws = new WebSocket(makeWsUrl());
            
            ws.onopen = () => {
                if (closed) {
                    ws?.close();
                    return;
                }
                setConnectionStatus('connected');
                retry = 0;
            };
            ws.onerror = () => setConnectionStatus('disconnected');
            ws.onclose = () => {
                setConnectionStatus('disconnected');
                if (closed) return;
                const delay = Math.min(5000, 500 * Math.pow(2, retry++));
                setTimeout(connect, delay);
            };
            
            ws.onmessage = (event) => {
                try {
                    const packet = JSON.parse(event.data);
                    setEvents(prev => [packet, ...prev].slice(0, 200));
                    setStats(s => ({ ...s, eventsCount: s.eventsCount + 1 }));
                    const ts = Date.parse(packet.created_at || packet.createdAt || "") || Date.now();
                    
                    if (packet.jobRequest || packet.payload?.job_request) {
                        setStats(s => ({ ...s, activeJobs: s.activeJobs + 1 }));
                        const jobId = packet.jobRequest?.jobId || packet.payload?.job_request?.job_id;
                        upsertJob(jobId, 'PENDING', ts);
                    }
                    if (packet.heartbeat || packet.payload?.heartbeat) {
                        const hb = packet.heartbeat || packet.payload?.heartbeat;
                        const workerId = hb.workerId || hb.worker_id;
                        if (workerId) {
                            setWorkers(prev => ({
                                ...prev,
                                [workerId]: {
                                    id: workerId,
                                    pool: hb.pool,
                                    activeJobs: hb.activeJobs || hb.active_jobs,
                                    maxParallelJobs: hb.maxParallelJobs || hb.max_parallel_jobs,
                                    capabilities: hb.capabilities || [],
                                    lastSeen: ts,
                                },
                            }));
                        }
                    }
                    if (packet.jobResult || packet.payload?.job_result) {
                        setStats(s => ({ ...s, activeJobs: Math.max(0, s.activeJobs - 1), completedJobs: s.completedJobs + 1 }));
                        const jr = packet.jobResult || packet.payload?.job_result;
                        const jobId = jr?.jobId || jr?.job_id;
                        const state = normalizeState(jr?.status);
                        upsertJob(jobId, state, ts);
                    }
                } catch (e) {
                    console.error("WS Parse Error", e);
                }
            };
        };

        connect();

        return () => {
            closed = true;
            if (ws && ws.readyState === WebSocket.OPEN) {
                ws.close();
            }
        };
    }, []);

    useEffect(() => {
        let cancelled = false;
        const loadHistory = async () => {
            setHistoryLoading(true);
            setHistoryError(null);
            try {
                const res = await apiFetch("/api/v1/jobs");
                if (!res.ok) throw new Error(`history fetch failed: ${res.status}`);
                const json = await res.json();
                if (cancelled) return;
                const normalized: JobRecord[] = (json || []).map((j: any) => ({
                    id: j.id || j.ID || j.job_id,
                    state: normalizeState(j.state || j.State),
                    updatedAt: Number(j.updatedAt || j.updated_at || j.updated || Date.now()),
                    traceId: j.traceId || j.trace_id,
                    safetyDecision: j.safety_decision || j.safetyDecision,
                    safetyReason: j.safety_reason || j.safetyReason,
                    topic: j.topic,
                    tenant: j.tenant,
                })).filter((rec: JobRecord) => !!rec.id);
                setJobHistory(normalized.sort((a, b) => (b.updatedAt ?? 0) - (a.updatedAt ?? 0)));
            } catch (err: any) {
                if (!cancelled) {
                    setHistoryError(err?.message || "failed to load history");
                }
            } finally {
                if (!cancelled) setHistoryLoading(false);
            }
        };
        loadHistory();
        const interval = setInterval(loadHistory, 30_000);
        return () => {
            cancelled = true;
            clearInterval(interval);
        };
    }, []);

    const derivedStats = useMemo(() => {
        const totals = jobHistory.reduce((acc, j): { active: number; completed: number; failed: number; byState: Record<string, number> } => {
            const st = normalizeState(j.state);
            acc.byState[st] = (acc.byState[st] || 0) + 1;
            if (st === 'RUNNING' || st === 'PENDING') acc.active += 1;
            if (st === 'COMPLETED') acc.completed += 1;
            if (st === 'FAILED') acc.failed += 1;
            return acc;
        }, { active: 0, completed: 0, failed: 0, byState: {} as Record<string, number> });
        return totals;
    }, [jobHistory]);

    const successRate = useMemo(() => {
        const total = jobHistory.length || 1;
        const completed = jobHistory.filter(j => normalizeState(j.state) === 'COMPLETED').length;
        return Math.round((completed / total) * 100);
    }, [jobHistory]);

    const filteredJobs = useMemo(() => {
        if (stateFilter === 'ALL') return jobHistory;
        return jobHistory.filter(j => normalizeState(j.state) === stateFilter);
    }, [jobHistory, stateFilter]);

    const eventsPerMinute = useMemo(() => {
        const now = Date.now();
        const recent = events.filter(e => {
            const ts = Date.parse((e as any).created_at || (e as any).createdAt || '') || now;
            return now - ts <= 5 * 60 * 1000;
        });
        return Math.max(0, Math.round((recent.length / 5)));
    }, [events]);

    const workerList = useMemo(() => Object.values(workers).sort((a, b) => b.lastSeen - a.lastSeen), [workers]);

    const refreshHistory = () => {
        // trigger useEffect by toggling state through fetch
        setHistoryLoading(true);
        apiFetch("/api/v1/jobs")
            .then(res => res.json())
            .then((json) => {
                const normalized: JobRecord[] = (json || []).map((j: any) => ({
                    id: j.id || j.ID || j.job_id,
                    state: normalizeState(j.state || j.State),
                    updatedAt: Number(j.updatedAt || j.updated_at || j.updated || Date.now()),
                    traceId: j.traceId || j.trace_id,
                    safetyDecision: j.safety_decision || j.safetyDecision,
                    safetyReason: j.safety_reason || j.safetyReason,
                    topic: j.topic,
                    tenant: j.tenant,
                })).filter((rec: JobRecord) => !!rec.id);
                setJobHistory(normalized.sort((a, b) => (b.updatedAt ?? 0) - (a.updatedAt ?? 0)));
            })
            .catch(err => setHistoryError(err?.message || "failed to load history"))
            .finally(() => setHistoryLoading(false));
    };

    const cancelJob = async (jobId: string) => {
        if (!jobId) return;
        setCanceling(prev => ({ ...prev, [jobId]: true }));
        try {
            const res = await apiFetch(`/api/v1/jobs/${jobId}/cancel`, { method: "POST" });
            const body = await res.json().catch(() => ({}));
            const nextState = normalizeState(body.state || 'CANCELLED');
            const reason = body.reason;
            if (!res.ok && res.status !== 409) {
                console.error("cancel job error", res.status, reason);
            }
            setJobHistory(prev =>
                prev.map(j =>
                    j.id === jobId
                        ? { ...j, state: nextState, updatedAt: Date.now(), safetyReason: reason || j.safetyReason }
                        : j
                )
            );
        } catch (err) {
            console.error("cancel job error", err);
            setJobHistory(prev =>
                prev.map(j =>
                    j.id === jobId ? { ...j, state: 'CANCELLED', updatedAt: Date.now() } : j
                )
            );
        } finally {
            setCanceling(prev => {
                const next = { ...prev };
                delete next[jobId];
                return next;
            });
        }
    };

    return (
        <div className="p-6 space-y-6">
            {/* Header */}
            <div className="flex justify-between items-center mb-6">
                <div>
                    <h2 className="text-2xl font-bold text-slate-100">Mission Control</h2>
                    <p className="text-sm text-slate-500">Real-time system observability</p>
                </div>
                <div className="flex gap-2">
                     <button className="px-3 py-1.5 text-xs font-medium bg-slate-800 hover:bg-slate-700 text-slate-300 rounded border border-slate-700 transition-colors">
                        1H
                    </button>
                    <button className="px-3 py-1.5 text-xs font-medium bg-indigo-600 text-white rounded shadow-sm shadow-indigo-500/20">
                        Live
                    </button>
                </div>
            </div>
            
            {/* KPI Grid */}
            <div className="grid gap-4 md:grid-cols-6">
                <StatCard 
                    title="Active Jobs" 
                    value={derivedStats.active || stats.activeJobs} 
                    sub="Running + Pending right now"
                    icon={Zap} 
                    colorClass="text-yellow-400 bg-yellow-400" 
                />
                <StatCard 
                    title="Completed" 
                    value={derivedStats.completed || stats.completedJobs} 
                    sub="Recent completions"
                    icon={Activity} 
                    colorClass="text-green-400 bg-green-400" 
                />
                <StatCard 
                    title="Success Rate" 
                    value={`${successRate || 0}%`} 
                    sub="Completed / All in window"
                    icon={Gauge} 
                    colorClass="text-emerald-300 bg-emerald-500/20" 
                />
                <StatCard 
                    title="Events/min" 
                    value={eventsPerMinute} 
                    sub="Bus throughput (last 5m avg)"
                    icon={Terminal} 
                    colorClass="text-purple-300 bg-purple-500/20" 
                />
                <StatCard 
                    title="Events" 
                    value={stats.eventsCount} 
                    sub="Total bus messages"
                    icon={Clock3} 
                    colorClass="text-purple-400 bg-purple-400" 
                />
                <StatCard 
                    title="Safety Denied" 
                    value={derivedStats.byState['DENIED'] || 0} 
                    sub="Blocked by kernel policies"
                    icon={AlertTriangle} 
                    colorClass="text-rose-300 bg-rose-500/20" 
                />
                <StatCard 
                    title="System Status" 
                    value={connectionStatus === 'connected' ? "ONLINE" : connectionStatus === 'connecting' ? "CONNECTING" : "DEGRADED"} 
                    sub={connectionStatus === 'connected' ? "Streaming from bus" : "Reconnecting to bus"}
                    icon={Radio} 
                    colorClass={connectionStatus === 'connected' ? "text-blue-300 bg-blue-500/20" : "text-amber-300 bg-amber-500/20"} 
                />
            </div>

            {/* Dashboard Main Layout */}
            <div className="grid grid-cols-1 xl:grid-cols-3 gap-6">
                
                {/* Recent Jobs / History */}
                <div className="xl:col-span-2 space-y-4">
                    <Card className="bg-slate-900 border-slate-800 shadow-sm">
                        <CardHeader className="py-4 px-6 border-b border-slate-800 flex flex-row items-center justify-between">
                            <div className="flex items-center gap-2">
                                <History size={16} className="text-slate-400" />
                                <CardTitle className="text-sm font-semibold text-slate-200">Recent Jobs (last 50)</CardTitle>
                            </div>
                            <div className="flex items-center gap-2 flex-wrap justify-end">
                                <div className="flex gap-1 rounded border border-slate-700 p-1 bg-slate-800/60">
                                    {(['ALL','RUNNING','PENDING','COMPLETED','FAILED'] as const).map(state => (
                                        <button
                                            key={state}
                                            className={clsx(
                                                "px-2 py-1 text-xs rounded transition",
                                                stateFilter === state ? "bg-slate-700 text-slate-100" : "text-slate-400 hover:text-slate-100"
                                            )}
                                            onClick={() => setStateFilter(state)}
                                        >
                                            {state}
                                        </button>
                                    ))}
                                </div>
                                {historyError && <span className="text-xs text-rose-400">{historyError}</span>}
                                <button
                                    className="text-xs flex items-center gap-1 text-slate-400 hover:text-slate-200 border border-slate-700 px-2 py-1 rounded"
                                    onClick={refreshHistory}
                                    disabled={historyLoading}
                                >
                                    {historyLoading ? <Loader2 size={12} className="animate-spin" /> : <RefreshCw size={12} />} Refresh
                                </button>
                            </div>
                        </CardHeader>
                        <CardContent className="p-0">
                            <div className="divide-y divide-slate-800">
                                {filteredJobs.slice(0, 20).map((job) => (
                                    <div key={job.id} className="px-6 py-3 flex items-center gap-4 hover:bg-slate-800/50 transition-colors">
                                        <div className={clsx("text-xs px-2 py-1 rounded-full font-semibold tracking-wide", statusColors[job.state] || statusColors.UNKNOWN)}>
                                            {job.state}
                                        </div>
                                        <div className="flex-1 min-w-0">
                                            <div className="text-sm font-medium text-slate-200 truncate">{job.id}</div>
                                            <div className="text-xs text-slate-500">Updated {formatAgo(job.updatedAt)}</div>
                                            {(job.state === 'DENIED' || job.state === 'FAILED') && job.safetyReason && (
                                                <div className="text-[11px] text-rose-300 mt-0.5 truncate">Reason: {job.safetyReason}</div>
                                            )}
                                        </div>
                                        {job.traceId && (
                                            <div className="text-[11px] text-slate-500 font-mono truncate max-w-[180px]">
                                                Trace {job.traceId}
                                            </div>
                                        )}
                                        {(job.state === 'RUNNING' || job.state === 'PENDING') && (
                                            <button
                                                className="text-xs px-2 py-1 rounded border border-rose-600 text-rose-200 hover:bg-rose-600/10 disabled:opacity-50"
                                                disabled={!!canceling[job.id]}
                                                onClick={() => cancelJob(job.id)}
                                            >
                                                {canceling[job.id] ? "Cancelling..." : "Cancel"}
                                            </button>
                                        )}
                                    </div>
                                ))}
                                {jobHistory.length === 0 && (
                                    <div className="px-6 py-10 text-center text-slate-500 text-sm">
                                        {historyLoading ? "Loading history..." : "No jobs yet. Kick off a workflow to see activity."}
                                    </div>
                                )}
                            </div>
                        </CardContent>
                    </Card>

                    {/* Live Event Stream */}
                    <Card className="bg-slate-900 border-slate-800 h-[420px] flex flex-col shadow-sm">
                        <CardHeader className="py-4 px-6 border-b border-slate-800 flex flex-row items-center justify-between">
                            <div className="flex items-center gap-2">
                                <Terminal size={16} className="text-slate-400" />
                                <CardTitle className="text-sm font-semibold text-slate-200">Live Bus Stream</CardTitle>
                            </div>
                            <span className={clsx(
                                "flex items-center gap-1 text-xs px-2 py-1 rounded-full border",
                                connectionStatus === 'connected' ? "border-emerald-500/50 text-emerald-300 bg-emerald-500/10" :
                                connectionStatus === 'connecting' ? "border-amber-400/50 text-amber-300 bg-amber-500/10" :
                                "border-rose-500/50 text-rose-300 bg-rose-500/10"
                            )}>
                                <Radio size={12} className={connectionStatus === 'connected' ? "animate-pulse" : ""} />
                                {connectionStatus.toUpperCase()}
                            </span>
                        </CardHeader>
                        <CardContent className="flex-1 overflow-hidden p-0 relative">
                            <div className="absolute inset-0 overflow-y-auto p-4 space-y-1 font-mono text-xs">
                                        {events.map((e, i) => (
                                            <div key={i} className="group flex items-start gap-3 hover:bg-slate-800/50 p-1 rounded -mx-1 px-2 transition-colors">
                                                <div className="text-slate-500 whitespace-nowrap">
                                                    {formatTime(e.created_at ? Date.parse(e.created_at) : Date.now())}
                                                </div>
                                                <div className={clsx(
                                                    "font-bold whitespace-nowrap min-w-[140px]",
                                                    (e.senderId || e.sender_id || '').includes('worker') ? 'text-green-400' :
                                                    (e.senderId || e.sender_id || '').includes('scheduler') ? 'text-purple-400' :
                                            'text-blue-400'
                                        )}>
                                            {e.senderId || e.sender_id}
                                        </div>
                                        <div className="text-slate-300 break-all opacity-80 group-hover:opacity-100">
                                            {JSON.stringify(e.jobRequest || e.jobResult || e.workerList || e.payload)}
                                        </div>
                                    </div>
                                ))}
                                {events.length === 0 && (
                                    <div className="flex flex-col items-center justify-center h-full text-slate-600">
                                        <Activity className={clsx("mb-2", connectionStatus === 'connecting' ? "animate-pulse" : "")} />
                                        <p>
                                            {connectionStatus === 'connecting' ? "Connecting to neural bus..." :
                                             connectionStatus === 'disconnected' ? "Connection lost. Reconnecting..." :
                                             "System idle. Waiting for events..."}
                                        </p>
                                    </div>
                                )}
                            </div>
                        </CardContent>
                    </Card>
                </div>

                {/* Incident rollup */}
                <div className="space-y-4">
                    <Card className="bg-slate-900 border-slate-800 shadow-sm">
                        <CardHeader className="py-4 px-6 border-b border-slate-800 flex items-center gap-2">
                            <Server size={16} className="text-blue-300" />
                            <CardTitle className="text-sm font-semibold text-slate-200">Worker Pools</CardTitle>
                        </CardHeader>
                        <CardContent className="space-y-3">
                            {workerList.length === 0 && (
                                <div className="text-xs text-slate-500">No heartbeats yet. Start workers to populate this panel.</div>
                            )}
                            {workerList.map((w) => (
                                <div key={w.id} className="rounded border border-slate-800 bg-slate-800/40 p-3">
                                    <div className="flex items-center justify-between text-sm text-slate-100">
                                        <span className="font-semibold">{w.id}</span>
                                        <span className="text-xs text-slate-400">{w.pool || 'unpooled'}</span>
                                    </div>
                                    <div className="mt-2 flex items-center justify-between text-xs text-slate-400">
                                        <span>Active {w.activeJobs ?? 0} / {w.maxParallelJobs ?? '?'}</span>
                                        <span>Seen {formatAgo(w.lastSeen)}</span>
                                    </div>
                                    {w.capabilities && w.capabilities.length > 0 && (
                                        <div className="mt-2 flex flex-wrap gap-1 text-[11px] text-slate-300">
                                            {w.capabilities.slice(0, 4).map((cap) => (
                                                <span key={cap} className="px-2 py-0.5 rounded-full bg-slate-700/60 border border-slate-600/60">
                                                    {cap}
                                                </span>
                                            ))}
                                        </div>
                                    )}
                                </div>
                            ))}
                        </CardContent>
                    </Card>

                    <Card className="bg-slate-900 border-slate-800 shadow-sm">
                        <CardHeader className="py-4 px-6 border-b border-slate-800">
                            <div className="flex items-center gap-2">
                                <AlertTriangle size={16} className="text-rose-400" />
                                <CardTitle className="text-sm font-semibold text-slate-200">Recent Failures / Denied</CardTitle>
                            </div>
                        </CardHeader>
                        <CardContent className="space-y-3">
                            {jobHistory.filter(j => j.state === 'FAILED' || j.state === 'DENIED').slice(0, 5).map(j => (
                                <div key={j.id} className="border border-rose-500/20 bg-rose-500/5 rounded p-3">
                                    <div className="text-sm text-rose-100 font-semibold truncate">{j.id}</div>
                                    <div className="text-xs text-rose-300">Updated {formatTime(j.updatedAt)}</div>
                                    {j.safetyReason && <div className="text-[11px] text-rose-200/80 mt-1 truncate">Reason: {j.safetyReason}</div>}
                                    {j.traceId && <div className="text-[11px] text-rose-200/80 mt-1">Trace {j.traceId}</div>}
                                </div>
                            ))}
                            {jobHistory.filter(j => j.state === 'FAILED' || j.state === 'DENIED').length === 0 && (
                                <div className="text-xs text-slate-500">No failed or denied jobs in the current window.</div>
                            )}
                        </CardContent>
                    </Card>

                    <Card className="bg-slate-900 border-slate-800 shadow-sm">
                        <CardHeader className="py-4 px-6 border-b border-slate-800 flex items-center gap-2">
                            <CheckCircle2 size={16} className="text-emerald-400" />
                            <CardTitle className="text-sm font-semibold text-slate-200">State Breakdown</CardTitle>
                        </CardHeader>
                        <CardContent className="space-y-2 text-sm">
                            {Object.entries(derivedStats.byState || {}).map(([state, count]) => (
                                <div key={state} className="flex items-center justify-between text-slate-300">
                                    <div className="flex items-center gap-2">
                                        <span className={clsx("h-2 w-2 rounded-full", state === 'FAILED' ? "bg-rose-400" : state === 'COMPLETED' ? "bg-emerald-400" : state === 'RUNNING' ? "bg-amber-400" : "bg-sky-400")} />
                                        {state}
                                    </div>
                                    <span className="font-mono text-slate-100">{count as number}</span>
                                </div>
                            ))}
                            {Object.keys(derivedStats.byState || {}).length === 0 && (
                                <div className="text-xs text-slate-500">No job history loaded yet.</div>
                            )}
                        </CardContent>
                    </Card>
                </div>
            </div>
        </div>
    );
};

export default MissionControl;

function normalizeState(raw?: string): string {
    if (!raw) return 'UNKNOWN';
    const s = raw.toString().toLowerCase();
    if (s.includes('fail')) return 'FAILED';
    if (s.includes('complete') || s.includes('done')) return 'COMPLETED';
    if (s.includes('run')) return 'RUNNING';
    if (s.includes('pend') || s.includes('queued')) return 'PENDING';
    return raw.toString().toUpperCase();
}

function formatTime(ts?: number) {
    if (!ts) return 'just now';
    const d = new Date(Number(ts));
    if (Number.isNaN(d.getTime())) return 'unknown';
    return d.toLocaleTimeString();
}

function formatAgo(ts?: number) {
    if (!ts) return 'just now';
    const diff = Date.now() - Number(ts);
    if (diff < 0) return 'just now';
    const mins = Math.floor(diff / 60000);
    if (mins <= 0) return 'just now';
    if (mins < 60) return `${mins}m ago`;
    const hours = Math.floor(mins / 60);
    if (hours < 24) return `${hours}h ago`;
    const days = Math.floor(hours / 24);
    return `${days}d ago`;
}
