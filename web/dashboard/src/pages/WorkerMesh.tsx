import { useEffect, useState } from 'react';
import { Card, CardContent } from '../components/ui/card';
import { apiFetch, type Worker } from '../lib/api';
import { Cpu, Box, Activity, Zap } from 'lucide-react';

const WorkerMesh = () => {
    const [workers, setWorkers] = useState<Worker[]>([]);
    const [loading, setLoading] = useState(true);

    const fetchWorkers = async () => {
        try {
            const res = await apiFetch(`/api/v1/workers`);
            if (res.ok) {
                const data = await res.json();
                const normalized = (data || []).map((w: any) => ({
                    workerId: w.workerId || w.worker_id,
                    type: w.type,
                    cpuLoad: w.cpuLoad ?? w.cpu_load ?? 0,
                    gpuUtilization: w.gpuUtilization ?? w.gpu_utilization ?? 0,
                    activeJobs: w.activeJobs ?? w.active_jobs ?? 0,
                    pool: w.pool,
                }));
                setWorkers(normalized);
            }
        } catch (e) {
            console.error(e);
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        fetchWorkers();
        const interval = setInterval(fetchWorkers, 5000);
        return () => clearInterval(interval);
    }, []);

    if (loading && workers.length === 0) {
        return <div className="p-8 text-slate-500 flex items-center gap-2"><Activity className="animate-spin" /> Scanning mesh...</div>;
    }

    return (
        <div className="p-6 space-y-6">
            <div className="flex justify-between items-center mb-6">
                <div>
                    <h2 className="text-2xl font-bold text-slate-100">Worker Mesh</h2>
                    <p className="text-sm text-slate-500">Active compute nodes topology</p>
                </div>
                <div className="flex items-center gap-2">
                     <span className="flex h-2 w-2 rounded-full bg-green-500 animate-pulse"></span>
                     <span className="text-sm font-mono text-green-400">{workers.length} ONLINE</span>
                </div>
            </div>

            <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                {workers.map((w) => (
                    <Card key={w.workerId} className="bg-slate-900 border-slate-800 shadow-sm hover:border-indigo-500/50 transition-colors group">
                        <CardContent className="p-5">
                            <div className="flex justify-between items-start mb-4">
                                <div className="flex items-center gap-3">
                                    <div className="p-2 bg-slate-800 rounded-lg group-hover:bg-indigo-900/20 group-hover:text-indigo-400 transition-colors text-slate-400">
                                        <ServerIcon type={w.type} />
                                    </div>
                                    <div>
                                        <div className="text-sm font-bold text-slate-200 font-mono truncate max-w-[120px]" title={w.workerId}>
                                            {w.workerId}
                                        </div>
                                        <div className="text-[10px] text-slate-500 uppercase tracking-wider font-semibold">
                                            {w.pool || w.type || 'generic'}
                                        </div>
                                    </div>
                                </div>
                                <div className="flex flex-col items-end">
                                    <span className="text-xs font-mono text-slate-400">LOAD</span>
                                    <span className={`text-lg font-bold ${w.cpuLoad > 80 ? 'text-red-400' : 'text-slate-200'}`}>
                                        {w.cpuLoad.toFixed(0)}%
                                    </span>
                                </div>
                            </div>
                            
                            {/* Visual Load Bar */}
                            <div className="h-1.5 w-full bg-slate-800 rounded-full mb-4 overflow-hidden">
                                <div 
                                    className={`h-full rounded-full ${w.cpuLoad > 80 ? 'bg-red-500' : 'bg-indigo-500'}`} 
                                    style={{ width: `${Math.min(w.cpuLoad, 100)}%` }}
                                ></div>
                            </div>

                            <div className="grid grid-cols-2 gap-2 mt-2">
                                <div className="bg-slate-950/50 p-2 rounded border border-slate-800/50">
                                    <div className="text-[10px] text-slate-500 uppercase flex items-center gap-1 mb-1">
                                        <Box size={10} /> Jobs
                                    </div>
                                    <div className="text-sm font-mono text-yellow-400 font-bold">
                                        {w.activeJobs}
                                    </div>
                                </div>
                                <div className="bg-slate-950/50 p-2 rounded border border-slate-800/50">
                                    <div className="text-[10px] text-slate-500 uppercase flex items-center gap-1 mb-1">
                                        <Zap size={10} /> GPU
                                    </div>
                                    <div className="text-sm font-mono text-purple-400 font-bold">
                                            {w.gpuUtilization}%
                                    </div>
                                </div>
                            </div>
                        </CardContent>
                    </Card>
                ))}
            </div>
        </div>
    );
};

const ServerIcon = ({ type }: { type: string }) => {
    if (type?.includes('gpu')) return <Cpu size={20} />;
    if (type?.includes('chat')) return <Activity size={20} />;
    return <Box size={20} />;
}

export default WorkerMesh;
