/*
 * DESIGN: "Control Surface" — Schemas Registry
 * PRD Section 21: Schema management with type filtering
 */
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { motion } from "framer-motion";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonTable } from "@/components/ui/Skeleton";
import { Search, Plus, FileJson } from "lucide-react";
import { useSchemas } from "@/hooks/useSchemas";

export default function SchemasPage() {
  const navigate = useNavigate();
  const [search, setSearch] = useState("");

  const { data, isLoading, error } = useSchemas();
  const schemas = data?.items ?? [];

  const filtered = schemas.filter(s => {
    if (!search) return true;
    const q = search.toLowerCase();
    return s.id.toLowerCase().includes(q) || (s.name ?? "").toLowerCase().includes(q);
  });

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader title="Schemas" subtitle="Define and manage data schemas for jobs and workflows" actions={<><Button variant="primary" size="sm" onClick={() => navigate("/schemas/new")}>
          <Plus className="w-3 h-3 mr-1" />Register Schema
        </Button></>} />

      {/* Search */}
      <div className="relative max-w-xs">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
        <input
          type="text"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search schemas..."
          className="h-8 w-full pl-9 pr-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
        />
      </div>

      {/* Table */}
      {isLoading ? (
        <SkeletonTable rows={6} />
      ) : error ? (
        <div className="instrument-card p-8 text-center">
          <p className="text-sm text-destructive">Failed to load schemas</p>
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState icon={<FileJson className="w-8 h-8" />} title="No schemas found" description="Register a schema to define data contracts" />
      ) : (
        <div className="instrument-card overflow-hidden">
          <div className="overflow-x-auto">
          <table className="w-full text-sm min-w-[400px]">
            <thead>
              <tr className="border-b border-border bg-surface-0">
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Name</th>
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Fields</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((schema, i) => (
                <motion.tr
                  key={schema.id}
                  initial={{ opacity: 0 }}
                  animate={{ opacity: 1 }}
                  transition={{ delay: i * 0.03 }}
                  onClick={() => navigate(`/schemas/${schema.id}`)}
                  className="border-b border-border last:border-0 hover:bg-surface-1 cursor-pointer transition-colors"
                >
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-2">
                      <FileJson className="w-3.5 h-3.5 text-cordum" />
                      <span className="font-medium text-foreground">{schema.name ?? schema.id}</span>
                    </div>
                  </td>
                  <td className="px-4 py-3 text-xs text-muted-foreground">{schema.fields?.length ?? 0} fields</td>
                </motion.tr>
              ))}
            </tbody>
          </table>
          </div>
        </div>
      )}
    </motion.div>
  );
}
