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
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Input } from "@/components/ui/Input";
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
      <div className="max-w-xs">
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search schemas..."
          icon={<Search className="w-3.5 h-3.5" />}
          className="h-8 bg-surface-1 text-xs"
        />
      </div>

      {/* Table */}
      {isLoading ? (
        <SkeletonTable rows={6} />
      ) : error ? (
        <ErrorBanner message={error instanceof Error ? error.message : "Failed to load schemas"} />
      ) : filtered.length === 0 ? (
        <EmptyState icon={<FileJson className="w-8 h-8" />} title="No schemas found" description="Register a JSON Schema to define data contracts for job inputs and outputs" action={<Button variant="primary" size="sm" onClick={() => navigate("/schemas/new")}><Plus className="w-3 h-3 mr-1" />Register schema</Button>} />
      ) : (
        <div className="instrument-card overflow-hidden">
          <div className="overflow-x-auto">
          <table className="w-full text-sm min-w-[400px]">
            <thead>
              <tr className="border-b border-border bg-surface-0">
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Name</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Fields</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((schema, i) => (
                <motion.tr
                  key={schema.id}
                  initial={{ opacity: 0 }}
                  animate={{ opacity: 1 }}
                  transition={{ delay: i * 0.03 }}
                  onClick={() => navigate(`/schemas/${encodeURIComponent(schema.id)}`)}
                  className="border-b border-border last:border-0 hover:bg-surface-1 cursor-pointer transition-colors"
                >
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-2">
                      <FileJson className="w-3.5 h-3.5 text-cordum" />
                      <span className="font-medium text-foreground">{schema.name ?? schema.id}</span>
                    </div>
                  </td>
                  <td className="px-5 py-3 text-xs text-muted-foreground">{schema.fields?.length ?? 0} fields</td>
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
