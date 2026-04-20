/*
 * DESIGN: "Control Surface" — Policy Analytics v2
 * Spec: Input + Output split, scope filter, evaluation trend, decision breakdown
 */
import { useState, useMemo } from "react";
import { motion } from "framer-motion";
import { PolicyStudioLayout } from "@/components/layout/PolicyStudioLayout";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { TrendingUp, Shield, XCircle, AlertTriangle, FileText, Eye } from "lucide-react";
import { AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer, PieChart, Pie, Cell } from "recharts";
import { cn } from "@/lib/utils";
import { usePolicyAudit } from "@/hooks/usePolicies";
import { useOutputPolicyStats } from "@/hooks/useOutputPolicy";

function categorizeAction(action: string): "allowed" | "blocked" | "warned" {
  const lower = action.toLowerCase();
  if (lower.includes("block") || lower.includes("deny") || lower.includes("denied") || lower.includes("reject")) return "blocked";
  if (lower.includes("warn") || lower.includes("approval")) return "warned";
  return "allowed";
}

function formatDay(timestamp: string): string {
  try { return new Date(timestamp).toLocaleDateString("en-US", { weekday: "short" }); } catch { return "?"; }
}

type ViewMode = "input" | "output";

export default function PoliciesAnalyticsPage() {
  const { data: auditData, isLoading: auditLoading, error } = usePolicyAudit();
  const { data: outputStats, isLoading: outputLoading } = useOutputPolicyStats();
  const auditEntries = auditData?.items ?? [];
  const [view, setView] = useState<ViewMode>("input");

  const { total, blockRate, approvalRate } = useMemo(() => {
    const t = auditEntries.length;
    const b = auditEntries.filter((e) => categorizeAction(e.action) === "blocked").length;
    const w = auditEntries.filter((e) => categorizeAction(e.action) === "warned").length;
    return { total: t, blockRate: t > 0 ? ((b / t) * 100).toFixed(1) : "--", approvalRate: t > 0 ? ((w / t) * 100).toFixed(1) : "--" };
  }, [auditEntries]);

  const trendData = useMemo(() => {
    const dayMap = new Map<string, { date: string; allowed: number; blocked: number; warned: number }>();
    for (const entry of auditEntries) {
      const day = formatDay(entry.timestamp);
      if (!dayMap.has(day)) dayMap.set(day, { date: day, allowed: 0, blocked: 0, warned: 0 });
      dayMap.get(day)![categorizeAction(entry.action)]++;
    }
    return Array.from(dayMap.values());
  }, [auditEntries]);

  const breakdownData = useMemo(() => {
    let allowed = 0, warned = 0, blocked = 0;
    for (const entry of auditEntries) {
      const cat = categorizeAction(entry.action);
      if (cat === "allowed") allowed++; else if (cat === "warned") warned++; else blocked++;
    }
    return [
      { name: "Allowed", value: allowed, color: "#00E5A0" },
      { name: "Warned", value: warned, color: "#F59E0B" },
      { name: "Blocked", value: blocked, color: "#EF4444" },
    ];
  }, [auditEntries]);

  const topBlocked = useMemo(() => {
    const blockedEntries = auditEntries.filter((e) => categorizeAction(e.action) === "blocked");
    const ruleMap = new Map<string, number>();
    for (const entry of blockedEntries) {
      const rule = entry.resourceName || entry.bundleId || "unknown";
      ruleMap.set(rule, (ruleMap.get(rule) ?? 0) + 1);
    }
    const sorted = Array.from(ruleMap.entries()).sort((a, b) => b[1] - a[1]).slice(0, 5);
    const maxCount = sorted.length > 0 ? sorted[0][1] : 1;
    return sorted.map(([rule, count]) => ({ rule, count, pct: Math.round((count / maxCount) * 100) }));
  }, [auditEntries]);

  const oStats = outputStats ?? { totalChecks24h: 0, quarantined24h: 0, avgLatencyMs: 0, lastCheckAt: undefined };
  const isLoading = auditLoading || outputLoading;

  return (
    <PolicyStudioLayout>
      <div className="space-y-6">
        {/* View toggle */}
        <div className="flex items-center gap-1 bg-surface-1 rounded-lg p-0.5 border border-border w-fit">
          <button onClick={() => setView("input")}
            className={cn("px-4 py-1.5 text-xs font-mono rounded-md transition-all", view === "input" ? "bg-cordum/15 text-cordum font-medium" : "text-muted-foreground hover:text-foreground")}>
            Input Policy
          </button>
          <button onClick={() => setView("output")}
            className={cn("px-4 py-1.5 text-xs font-mono rounded-md transition-all", view === "output" ? "bg-cordum/15 text-cordum font-medium" : "text-muted-foreground hover:text-foreground")}>
            Output Policy
          </button>
        </div>

        {isLoading ? (
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4">{[1, 2, 3, 4].map((i) => <SkeletonCard key={i} />)}</div>
        ) : error ? (
          <div className="instrument-card p-8 text-center">
            <AlertTriangle className="w-8 h-8 text-red-400 mx-auto mb-3" />
            <p className="text-sm text-foreground font-medium mb-1">Failed to load analytics</p>
            <p className="text-xs text-muted-foreground">{error instanceof Error ? error.message : "An unexpected error occurred"}</p>
          </div>
        ) : (
          <>
            {/* KPIs */}
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
              {(view === "input"
                ? [
                    { label: "Total Evaluations", value: total.toLocaleString(), icon: Shield },
                    { label: "Block Rate", value: blockRate === "--" ? "--" : `${blockRate}%`, icon: XCircle },
                    { label: "Approval Rate", value: approvalRate === "--" ? "--" : `${approvalRate}%`, icon: AlertTriangle },
                    { label: "Avg Latency", value: "\u2014", icon: TrendingUp },
                  ]
                : [
                    { label: "Total Checks (24h)", value: oStats.totalChecks24h.toLocaleString(), icon: Eye },
                    { label: "Quarantined (24h)", value: oStats.quarantined24h.toLocaleString(), icon: XCircle },
                    { label: "Avg Latency", value: `${oStats.avgLatencyMs}ms`, icon: TrendingUp },
                    { label: "Last Check", value: oStats.lastCheckAt ? new Date(oStats.lastCheckAt).toLocaleTimeString() : "—", icon: Shield },
                  ]
              ).map((kpi, i) => {
                const Icon = kpi.icon;
                return (
                  <motion.div key={kpi.label} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: i * 0.05 }}
                    className="instrument-card p-5">
                    <div className="flex items-center justify-between mb-2">
                      <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">{kpi.label}</span>
                      <Icon className="w-4 h-4 text-muted-foreground" />
                    </div>
                    <span className="text-2xl font-mono font-bold text-foreground">{kpi.value}</span>
                  </motion.div>
                );
              })}
            </div>

            {view === "input" ? (
              <>
                {/* Trend Chart */}
                <div className="instrument-card p-5">
                  <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider mb-4">Evaluation Trend</p>
                  {trendData.length === 0 ? (
                    <p className="text-xs text-muted-foreground text-center py-8">No evaluation data available</p>
                  ) : (
                    <div className="h-64">
                      <ResponsiveContainer width="100%" height="100%">
                        <AreaChart data={trendData}>
                          <XAxis dataKey="date" tick={{ fill: "#6B7280", fontSize: 10 }} axisLine={false} tickLine={false} />
                          <YAxis tick={{ fill: "#6B7280", fontSize: 10 }} axisLine={false} tickLine={false} />
                          <Tooltip contentStyle={{ background: "#1E293B", border: "1px solid rgba(255,255,255,0.1)", borderRadius: "8px", fontSize: "12px" }} />
                          <Area type="monotone" dataKey="allowed" stackId="1" stroke="#00E5A0" fill="#00E5A0" fillOpacity={0.2} />
                          <Area type="monotone" dataKey="warned" stackId="1" stroke="#F59E0B" fill="#F59E0B" fillOpacity={0.2} />
                          <Area type="monotone" dataKey="blocked" stackId="1" stroke="#EF4444" fill="#EF4444" fillOpacity={0.2} />
                        </AreaChart>
                      </ResponsiveContainer>
                    </div>
                  )}
                </div>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <div className="instrument-card p-5">
                    <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider mb-4">Decision Breakdown</p>
                    {total === 0 ? (
                      <p className="text-xs text-muted-foreground text-center py-8">No evaluation data available</p>
                    ) : (
                      <>
                        <div className="h-48">
                          <ResponsiveContainer width="100%" height="100%">
                            <PieChart>
                              <Pie data={breakdownData} cx="50%" cy="50%" innerRadius={50} outerRadius={80} paddingAngle={3} dataKey="value">
                                {breakdownData.map((entry) => <Cell key={entry.name} fill={entry.color} />)}
                              </Pie>
                              <Tooltip contentStyle={{ background: "#1E293B", border: "1px solid rgba(255,255,255,0.1)", borderRadius: "8px", fontSize: "12px" }} />
                            </PieChart>
                          </ResponsiveContainer>
                        </div>
                        <div className="flex justify-center gap-4 mt-2">
                          {breakdownData.map((b) => (
                            <div key={b.name} className="flex items-center gap-1.5">
                              <div className="w-2 h-2 rounded-full" style={{ background: b.color }} />
                              <span className="text-[10px] text-muted-foreground">{b.name} ({b.value})</span>
                            </div>
                          ))}
                        </div>
                      </>
                    )}
                  </div>
                  <div className="instrument-card p-5">
                    <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider mb-4">Top Blocked Rules</p>
                    {topBlocked.length === 0 ? (
                      <p className="text-xs text-muted-foreground text-center py-8">No blocks in audit window</p>
                    ) : (
                      <div className="space-y-3">
                        {topBlocked.map((r, i) => (
                          <div key={r.rule}>
                            <div className="flex items-center justify-between mb-1">
                              <span className="text-xs font-mono text-foreground">{r.rule}</span>
                              <span className="text-xs text-muted-foreground">{r.count} blocks</span>
                            </div>
                            <div className="h-1.5 rounded-full bg-surface-2 overflow-hidden">
                              <motion.div className="h-full rounded-full bg-red-400" initial={{ width: 0 }} animate={{ width: `${r.pct}%` }}
                                transition={{ duration: 0.6, delay: i * 0.1 }} />
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                </div>
              </>
            ) : (
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div className="instrument-card p-5">
                  <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider mb-4">Output Scan Breakdown</p>
                  {oStats.totalChecks24h === 0 ? (
                    <p className="text-xs text-muted-foreground text-center py-8">No output scan data available</p>
                  ) : (
                    <>
                      <div className="h-48">
                        <ResponsiveContainer width="100%" height="100%">
                          <PieChart>
                            <Pie data={[
                              { name: "Passed", value: Math.max(0, oStats.totalChecks24h - oStats.quarantined24h), color: "#00E5A0" },
                              { name: "Quarantined", value: oStats.quarantined24h, color: "#EF4444" },
                            ]} cx="50%" cy="50%" innerRadius={50} outerRadius={80} paddingAngle={3} dataKey="value">
                              <Cell fill="#00E5A0" /><Cell fill="#EF4444" />
                            </Pie>
                            <Tooltip contentStyle={{ background: "#1E293B", border: "1px solid rgba(255,255,255,0.1)", borderRadius: "8px", fontSize: "12px" }} />
                          </PieChart>
                        </ResponsiveContainer>
                      </div>
                      <div className="flex justify-center gap-4 mt-2">
                        <div className="flex items-center gap-1.5"><div className="w-2 h-2 rounded-full bg-emerald-400" /><span className="text-[10px] text-muted-foreground">Passed ({Math.max(0, oStats.totalChecks24h - oStats.quarantined24h)})</span></div>
                        <div className="flex items-center gap-1.5"><div className="w-2 h-2 rounded-full bg-red-400" /><span className="text-[10px] text-muted-foreground">Quarantined ({oStats.quarantined24h})</span></div>
                      </div>
                    </>
                  )}
                </div>
                <div className="instrument-card p-5">
                  <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider mb-4">Scanner Performance</p>
                  <p className="text-xs text-muted-foreground text-center py-8">Connect to a live Cordum instance to see scanner metrics</p>
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </PolicyStudioLayout>
  );
}
