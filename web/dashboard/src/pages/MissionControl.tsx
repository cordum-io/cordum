import { useEffect, useState } from 'react';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import { Activity, Server, Zap, Cpu, Terminal, Filter } from 'lucide-react';
import { makeWsUrl, type BusPacket } from '../lib/api';
import clsx from 'clsx';

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
    const [stats, setStats] = useState({
        activeJobs: 0,
        completedJobs: 0,
        eventsCount: 0
    });
    const [connectionStatus, setConnectionStatus] = useState<'connecting' | 'connected' | 'disconnected'>('connecting');

    useEffect(() => {
        const ws = new WebSocket(makeWsUrl());
        
        ws.onopen = () => setConnectionStatus('connected');
        ws.onerror = () => setConnectionStatus('disconnected');
        ws.onclose = () => setConnectionStatus('disconnected');
        
        ws.onmessage = (event) => {
            try {
                const packet = JSON.parse(event.data);
                setEvents(prev => [packet, ...prev].slice(0, 100));
                setStats(s => ({ ...s, eventsCount: s.eventsCount + 1 }));
                
                if (packet.jobRequest || packet.payload?.job_request) {
                    setStats(s => ({ ...s, activeJobs: s.activeJobs + 1 }));
                }
                 if (packet.jobResult || packet.payload?.job_result) {
                    setStats(s => ({ ...s, activeJobs: Math.max(0, s.activeJobs - 1), completedJobs: s.completedJobs + 1 }));
                }
            } catch (e) {
                console.error("WS Parse Error", e);
            }
        };

        return () => ws.close();
    }, []);

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
            <div className="grid gap-4 md:grid-cols-4">
                <StatCard 
                    title="Active Jobs" 
                    value={stats.activeJobs} 
                    sub="Current concurrency"
                    icon={Zap} 
                    colorClass="text-yellow-400 bg-yellow-400" 
                />
                <StatCard 
                    title="Completed" 
                    value={stats.completedJobs} 
                    sub="Since session start"
                    icon={Activity} 
                    colorClass="text-green-400 bg-green-400" 
                />
                 <StatCard 
                    title="Events" 
                    value={stats.eventsCount} 
                    sub="Total bus messages"
                    icon={Terminal} 
                    colorClass="text-purple-400 bg-purple-400" 
                />
                <StatCard 
                    title="System Status" 
                    value="HEALTHY" 
                    sub="All systems operational"
                    icon={Server} 
                    colorClass="text-blue-400 bg-blue-400" 
                />
            </div>

            {/* Dashboard Main Layout */}
            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
                
                {/* Live Event Stream */}
                <div className="lg:col-span-3">
                    <Card className="bg-slate-900 border-slate-800 h-[500px] flex flex-col shadow-sm">
                        <CardHeader className="py-4 px-6 border-b border-slate-800 flex flex-row items-center justify-between">
                            <div className="flex items-center gap-2">
                                <Terminal size={16} className="text-slate-400" />
                                <CardTitle className="text-sm font-semibold text-slate-200">Live Log Stream</CardTitle>
                            </div>
                            <button className="text-xs flex items-center gap-1 text-slate-500 hover:text-slate-300">
                                <Filter size={12} /> Filter
                            </button>
                        </CardHeader>
                        <CardContent className="flex-1 overflow-hidden p-0 relative">
                            <div className="absolute inset-0 overflow-y-auto p-4 space-y-1 font-mono text-xs">
                                {events.map((e, i) => (
                                    <div key={i} className="group flex items-start gap-3 hover:bg-slate-800/50 p-1 rounded -mx-1 px-2 transition-colors">
                                        <div className="text-slate-500 whitespace-nowrap">
                                            {new Date(e.created_at || Date.now()).toLocaleTimeString()}
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
            </div>
        </div>
    );
};

export default MissionControl;
