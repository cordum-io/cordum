/*
 * DESIGN: "Control Surface" — Notification Settings
 * PRD Section 33: Channels + event preferences matrix
 */
import { useState } from "react";
import { useQuery, useMutation } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { get, post } from "@/api/client";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { SkeletonCard, SkeletonTable } from "@/components/ui/Skeleton";
import { Bell, Mail, MessageSquare, Webhook, Save, Plus, TestTube } from "lucide-react";
import { cn } from "@/lib/utils";
import { toast } from "sonner";

interface Channel {
  id: string;
  type: "email" | "slack" | "webhook";
  name: string;
  target: string;
  enabled: boolean;
}

const EVENTS = [
  { key: "job.failed", label: "Job Failed", category: "Jobs" },
  { key: "job.timeout", label: "Job Timeout", category: "Jobs" },
  { key: "approval.pending", label: "Approval Pending", category: "Approvals" },
  { key: "approval.expired", label: "Approval Expired", category: "Approvals" },
  { key: "worker.offline", label: "Worker Offline", category: "Agents" },
  { key: "safety.blocked", label: "Safety Blocked", category: "Safety" },
  { key: "dlq.threshold", label: "DLQ Threshold", category: "System" },
  { key: "health.degraded", label: "Service Degraded", category: "System" },
];

export default function SettingsNotificationsPage() {
  const [activeTab, setActiveTab] = useState("events");
  const [preferences, setPreferences] = useState<Record<string, boolean>>(() => {
    const init: Record<string, boolean> = {};
    EVENTS.forEach(e => { init[e.key] = true; });
    return init;
  });

  const { data: channels, isLoading } = useQuery({
    queryKey: ["notification-channels"],
    queryFn: async () => {
      const res = await get<{ data?: Channel[] }>("/notifications/channels");
      return res.data || [];
    },
  });

  const saveMutation = useMutation({
    mutationFn: async () => post("/notifications/preferences", preferences),
    onSuccess: () => toast.success("Preferences saved"),
  });

  const channelIcon = (type: string) => {
    switch (type) {
      case "email": return Mail;
      case "slack": return MessageSquare;
      case "webhook": return Webhook;
      default: return Bell;
    }
  };

  const tabs = ["events", "channels"];
  const categories = [...new Set(EVENTS.map(e => e.category))];

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader title="Notifications" subtitle="Configure alert channels and event preferences" />

      <div className="flex items-center gap-1 p-1 rounded-lg bg-surface-1 w-fit">
        {tabs.map(tab => (
          <button key={tab} onClick={() => setActiveTab(tab)}
            className={cn("px-4 py-1.5 text-xs font-medium rounded-md transition-colors capitalize",
              activeTab === tab ? "bg-cordum/10 text-cordum" : "text-muted-foreground hover:text-foreground")}>
            {tab}
          </button>
        ))}
      </div>

      {/* Events Tab */}
      {activeTab === "events" && (
        <div className="space-y-4">
          {categories.map(cat => (
            <div key={cat} className="instrument-card overflow-hidden">
              <div className="px-4 py-3 bg-surface-0 border-b border-border">
                <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">{cat}</p>
              </div>
              <div className="divide-y divide-border">
                {EVENTS.filter(e => e.category === cat).map(event => (
                  <div key={event.key} className="flex items-center justify-between px-4 py-3 hover:bg-surface-1 transition-colors">
                    <div>
                      <p className="text-xs font-medium text-foreground">{event.label}</p>
                      <p className="text-[10px] font-mono text-muted-foreground">{event.key}</p>
                    </div>
                    <button
                      onClick={() => setPreferences(prev => ({ ...prev, [event.key]: !prev[event.key] }))}
                      className={cn("w-9 h-5 rounded-full relative transition-colors",
                        preferences[event.key] ? "bg-cordum" : "bg-surface-2")}>
                      <div className={cn("absolute top-0.5 w-4 h-4 rounded-full bg-white transition-transform",
                        preferences[event.key] ? "left-[18px]" : "left-0.5")} />
                    </button>
                  </div>
                ))}
              </div>
            </div>
          ))}
          <Button variant="primary" size="sm" onClick={() => saveMutation.mutate()} loading={saveMutation.isPending}>
            <Save className="w-3 h-3 mr-1" />Save Preferences
          </Button>
        </div>
      )}

      {/* Channels Tab */}
      {activeTab === "channels" && (
        isLoading ? <div className="space-y-3">{Array.from({ length: 3 }).map((_, i) => <SkeletonCard key={i} />)}</div> : (
          <div className="space-y-3">
            {(channels || []).map((ch, i) => {
              const Icon = channelIcon(ch.type);
              return (
                <motion.div key={ch.id} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: i * 0.05 }}
                  className="instrument-card p-4 flex items-center justify-between">
                  <div className="flex items-center gap-3">
                    <Icon className="w-4 h-4 text-cordum" />
                    <div>
                      <span className="text-sm font-medium text-foreground">{ch.name}</span>
                      <p className="text-xs font-mono text-muted-foreground">{ch.target}</p>
                    </div>
                  </div>
                  <div className="flex items-center gap-3">
                    <StatusBadge variant={ch.enabled ? "healthy" : "muted"}>{ch.enabled ? "Active" : "Disabled"}</StatusBadge>
                    <Button variant="ghost" size="sm" onClick={() => toast.info("Test notification sent")}>
                      <TestTube className="w-3 h-3 mr-1" />Test
                    </Button>
                  </div>
                </motion.div>
              );
            })}
            <Button variant="outline" size="sm" onClick={() => toast.info("Feature coming soon")}>
              <Plus className="w-3 h-3 mr-1" />Add Channel
            </Button>
          </div>
        )
      )}
    </motion.div>
  );
}
