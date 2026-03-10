/*
 * DESIGN: "Control Surface" — Schema Detail
 * PRD Section 22: Schema version history and field definitions
 */
import { useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { get } from "@/api/client";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { ArrowLeft, FileJson, Copy, Clock, Hash, Edit } from "lucide-react";
import { cn, formatRelativeTime } from "@/lib/utils";
import { toast } from "sonner";

interface SchemaField {
  name: string;
  type: string;
  required: boolean;
  description?: string;
}

interface SchemaVersion {
  version: string;
  createdAt: string;
  fields: SchemaField[];
  changelog?: string;
}

export default function SchemaDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const [activeTab, setActiveTab] = useState("fields");

  const isCreateMode = id === "new";

  const { data: schema, isLoading } = useQuery({
    queryKey: ["schema", id],
    queryFn: async () => {
      const res = await get<{ data?: { id: string; name: string; type: string; versions: SchemaVersion[]; currentVersion: string } }>(`/schemas/${id}`);
      return res.data;
    },
    enabled: !isCreateMode,
  });

  const tabs = ["fields", "versions", "json"];
  const currentVersion = schema?.versions?.find(v => v.version === schema.currentVersion) || schema?.versions?.[0];

  if (isCreateMode) {
    return (
      <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <button onClick={() => navigate("/schemas")} className="p-1.5 rounded-full hover:bg-surface-2 transition-colors">
              <ArrowLeft className="w-4 h-4 text-muted-foreground" />
            </button>
            <FileJson className="w-5 h-5 text-cordum" />
            <div>
              <h1 className="text-lg font-display font-bold text-foreground">New Schema</h1>
              <p className="text-xs text-muted-foreground">Define a new schema for your platform</p>
            </div>
          </div>
        </div>
        <div className="instrument-card p-6">
          <p className="text-sm text-muted-foreground">Select a schema from the list to view its details.</p>
          <Button variant="outline" size="sm" className="mt-4" onClick={() => navigate("/schemas")}>
            Back to Schemas
          </Button>
        </div>
      </motion.div>
    );
  }

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div className="flex items-center gap-3">
          <div className="h-8 w-8 rounded bg-surface-2 animate-pulse" />
          <div className="h-6 w-48 rounded bg-surface-2 animate-pulse" />
        </div>
        {Array.from({ length: 3 }).map((_, i) => <SkeletonCard key={i} />)}
      </div>
    );
  }

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <button onClick={() => navigate("/schemas")} className="p-1.5 rounded-full hover:bg-surface-2 transition-colors">
            <ArrowLeft className="w-4 h-4 text-muted-foreground" />
          </button>
          <FileJson className="w-5 h-5 text-cordum" />
          <div>
            <h1 className="text-lg font-display font-bold text-foreground">{schema?.name || id}</h1>
            <div className="flex items-center gap-2 mt-0.5">
              <StatusBadge variant="info">{schema?.type}</StatusBadge>
              <span className="text-xs font-mono text-muted-foreground">v{schema?.currentVersion}</span>
            </div>
          </div>
        </div>
        <Button variant="outline" size="sm" onClick={() => toast.info("Feature coming soon")}>
          <Edit className="w-3 h-3 mr-1" />Edit Schema
        </Button>
      </div>

      {/* Tabs */}
      <div className="flex items-center gap-1 p-1 rounded-2xl bg-surface-1 w-fit">
        {tabs.map(tab => (
          <button
            key={tab}
            onClick={() => setActiveTab(tab)}
            className={cn(
              "px-4 py-1.5 text-xs font-medium rounded-2xl transition-colors capitalize",
              activeTab === tab ? "bg-cordum/10 text-cordum" : "text-muted-foreground hover:text-foreground",
            )}
          >
            {tab === "json" ? "JSON" : tab}
          </button>
        ))}
      </div>

      {/* Fields Tab */}
      {activeTab === "fields" && currentVersion && (
        <div className="instrument-card overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-surface-0">
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Field</th>
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Type</th>
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Required</th>
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Description</th>
              </tr>
            </thead>
            <tbody>
              {(currentVersion.fields || []).map((field, i) => (
                <tr key={field.name} className="border-b border-border last:border-0 hover:bg-surface-1 transition-colors">
                  <td className="px-4 py-3 font-mono text-xs text-foreground">{field.name}</td>
                  <td className="px-4 py-3"><StatusBadge variant="info">{field.type}</StatusBadge></td>
                  <td className="px-4 py-3">{field.required ? <StatusBadge variant="warning">required</StatusBadge> : <span className="text-xs text-muted-foreground">optional</span>}</td>
                  <td className="px-4 py-3 text-xs text-muted-foreground">{field.description || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Versions Tab */}
      {activeTab === "versions" && (
        <div className="space-y-3">
          {(schema?.versions || []).map((v, i) => (
            <motion.div
              key={v.version}
              initial={{ opacity: 0, y: 8 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: i * 0.05 }}
              className={cn("instrument-card p-4", v.version === schema?.currentVersion && "status-healthy")}
            >
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <Hash className="w-3.5 h-3.5 text-cordum" />
                  <span className="text-sm font-mono font-semibold text-foreground">v{v.version}</span>
                  {v.version === schema?.currentVersion && <StatusBadge variant="healthy">current</StatusBadge>}
                </div>
                <span className="text-xs text-muted-foreground flex items-center gap-1">
                  <Clock className="w-3 h-3" />{formatRelativeTime(v.createdAt)}
                </span>
              </div>
              {v.changelog && <p className="text-xs text-muted-foreground mt-2">{v.changelog}</p>}
              <p className="text-[10px] text-muted-foreground mt-1">{v.fields.length} fields</p>
            </motion.div>
          ))}
        </div>
      )}

      {/* JSON Tab */}
      {activeTab === "json" && currentVersion && (
        <div className="instrument-card p-0 overflow-hidden">
          <div className="flex items-center justify-between px-4 py-2 bg-surface-0 border-b border-border">
            <span className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">JSON Schema</span>
            <button
              onClick={() => { navigator.clipboard.writeText(JSON.stringify(currentVersion, null, 2)); toast.success("Copied"); }}
              className="p-1 rounded hover:bg-surface-2 transition-colors"
            >
              <Copy className="w-3 h-3 text-muted-foreground" />
            </button>
          </div>
          <pre className="p-4 text-xs font-mono text-foreground overflow-auto max-h-96">
            {JSON.stringify(currentVersion, null, 2)}
          </pre>
        </div>
      )}
    </motion.div>
  );
}
