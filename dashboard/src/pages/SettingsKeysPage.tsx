/*
 * DESIGN: "Control Surface" — API Keys
 * PRD Section 31: API key management with create/revoke
 */
import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { get, post, del } from "@/api/client";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonTable } from "@/components/ui/Skeleton";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { DialogOverlay } from "@/components/ui/DialogOverlay";
import { Key, Plus, Copy, Trash2, X } from "lucide-react";
import { cn, formatRelativeTime } from "@/lib/utils";
import { toast } from "sonner";

interface ApiKey {
  id: string;
  name: string;
  prefix: string;
  createdAt: string;
  lastUsed?: string;
  scopes: string[];
}

interface InvalidateQueriesClient {
  invalidateQueries: (options: { queryKey: string[] }) => unknown;
}

interface CreateKeyMutationDeps {
  queryClient: InvalidateQueriesClient;
  setCreatedKey: (key: string | null) => void;
  setNewKeyName: (name: string) => void;
}

interface DeleteKeyMutationDeps {
  queryClient: InvalidateQueriesClient;
  setDeleteTarget: (key: ApiKey | null) => void;
}

function errorDescription(err: unknown): string {
  if (err instanceof Error && err.message.trim()) {
    return err.message;
  }
  if (typeof err === "string" && err.trim()) {
    return err;
  }
  return "Unknown error";
}

export function handleCreateKeySuccess(data: { data?: { key?: string } } | undefined, deps: CreateKeyMutationDeps) {
  deps.queryClient.invalidateQueries({ queryKey: ["api-keys"] });
  const key = data?.data?.key;
  if (key) {
    deps.setCreatedKey(key);
  } else {
    toast.error("API key created but key value not returned");
  }
  deps.setNewKeyName("");
}

export function handleCreateKeyError(err: unknown) {
  toast.error("Failed to create API key", { description: errorDescription(err) });
}

export function handleDeleteKeySuccess(deps: DeleteKeyMutationDeps) {
  deps.queryClient.invalidateQueries({ queryKey: ["api-keys"] });
  toast.success("API key revoked");
  deps.setDeleteTarget(null);
}

export function handleDeleteKeyError(err: unknown) {
  toast.error("Failed to revoke API key", { description: errorDescription(err) });
}

export default function SettingsKeysPage() {
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [newKeyName, setNewKeyName] = useState("");
  const [newKeyScopes, setNewKeyScopes] = useState<string[]>(["read"]);
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ApiKey | null>(null);

  const { data: keys, isLoading } = useQuery({
    queryKey: ["api-keys"],
    queryFn: async () => {
      const res = await get<{ data?: ApiKey[] }>("/auth/keys");
      return res.data || [];
    },
  });

  const createMutation = useMutation({
    mutationFn: async () => post<{ data?: { key?: string } }>("/auth/keys", { name: newKeyName, scopes: newKeyScopes }),
    onSuccess: (data) => handleCreateKeySuccess(data, { queryClient, setCreatedKey, setNewKeyName }),
    onError: handleCreateKeyError,
  });

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => del(`/auth/keys/${id}`),
    onSuccess: () => handleDeleteKeySuccess({ queryClient, setDeleteTarget }),
    onError: handleDeleteKeyError,
  });

  const SCOPES = ["read", "write", "admin"];

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader title="API Keys" subtitle="Manage API keys for programmatic access" actions={<><Button variant="primary" size="sm" onClick={() => { setCreateOpen(true); setCreatedKey(null); }}>
          <Plus className="w-3 h-3 mr-1" />Create Key
        </Button></>} />

      {isLoading ? <SkeletonTable rows={4} /> :
       !keys?.length ? <EmptyState icon={<Key className="w-8 h-8" />} title="No API keys" description="Create an API key to access the Cordum API" /> : (
        <div className="instrument-card overflow-hidden">
          <div className="overflow-x-auto">
          <table className="w-full text-sm min-w-[600px]">
            <thead>
              <tr className="border-b border-border bg-surface-0">
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Name</th>
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Key</th>
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Scopes</th>
                <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Last Used</th>
                <th className="text-right px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Actions</th>
              </tr>
            </thead>
            <tbody>
              {keys.map((key, i) => (
                <motion.tr key={key.id} initial={{ opacity: 0 }} animate={{ opacity: 1 }} transition={{ delay: i * 0.03 }}
                  className="border-b border-border last:border-0 hover:bg-surface-1 transition-colors">
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-2">
                      <Key className="w-3.5 h-3.5 text-cordum" />
                      <span className="text-sm font-medium text-foreground">{key.name}</span>
                    </div>
                  </td>
                  <td className="px-4 py-3 font-mono text-xs text-muted-foreground">{key.prefix}...****</td>
                  <td className="px-4 py-3">
                    <div className="flex gap-1">{key.scopes.map(s => <StatusBadge key={s} variant="info">{s}</StatusBadge>)}</div>
                  </td>
                  <td className="px-4 py-3 text-xs text-muted-foreground">{key.lastUsed ? formatRelativeTime(key.lastUsed) : "Never"}</td>
                  <td className="px-4 py-3 text-right">
                    <button onClick={() => setDeleteTarget(key)} className="p-1.5 rounded hover:bg-destructive/10 transition-colors">
                      <Trash2 className="w-3.5 h-3.5 text-destructive" />
                    </button>
                  </td>
                </motion.tr>
              ))}
            </tbody>
          </table>
          </div>
        </div>
      )}

      {/* Create Dialog */}
      <DialogOverlay open={createOpen} onClose={() => setCreateOpen(false)} label={createdKey ? "Key Created" : "Create API key"} className="w-[420px] bg-surface-1 border border-border rounded-xl shadow-2xl p-6">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-sm font-display font-semibold text-foreground">{createdKey ? "Key Created" : "Create API Key"}</h3>
          <button onClick={() => setCreateOpen(false)} className="p-1 rounded hover:bg-surface-2"><X className="w-4 h-4 text-muted-foreground" /></button>
        </div>
        {createdKey ? (
          <div className="space-y-4">
            <div className="p-3 bg-[var(--color-warning)]/10 border border-[var(--color-warning)]/20 rounded-2xl">
              <p className="text-xs text-[var(--color-warning)] mb-2">Copy this key now — it won't be shown again.</p>
              <div className="flex items-center gap-2">
                <code className="flex-1 text-xs font-mono text-foreground bg-surface-2 px-3 py-2 rounded">{createdKey}</code>
                <button onClick={() => { navigator.clipboard.writeText(createdKey); toast.success("Copied"); }} className="p-2 rounded hover:bg-surface-2">
                  <Copy className="w-3.5 h-3.5 text-muted-foreground" />
                </button>
              </div>
            </div>
            <Button variant="primary" size="sm" className="w-full" onClick={() => setCreateOpen(false)}>Done</Button>
          </div>
        ) : (
          <div className="space-y-4">
            <div>
              <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Name</label>
              <input type="text" value={newKeyName} onChange={(e) => setNewKeyName(e.target.value)} placeholder="e.g., CI Pipeline"
                className="h-9 w-full px-3 text-sm bg-surface-2 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum" />
            </div>
            <div>
              <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Scopes</label>
              <div className="flex gap-2">
                {SCOPES.map(s => (
                  <button key={s} onClick={() => setNewKeyScopes(prev => prev.includes(s) ? prev.filter(x => x !== s) : [...prev, s])}
                    className={cn("px-3 py-1.5 text-xs rounded-2xl border transition-colors capitalize",
                      newKeyScopes.includes(s) ? "bg-cordum/10 border-cordum/30 text-cordum" : "border-border text-muted-foreground hover:text-foreground")}>
                    {s}
                  </button>
                ))}
              </div>
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button variant="ghost" size="sm" onClick={() => setCreateOpen(false)}>Cancel</Button>
              <Button variant="primary" size="sm" onClick={() => createMutation.mutate()} loading={createMutation.isPending} disabled={!newKeyName.trim()}>
                <Key className="w-3 h-3 mr-1" />Create
              </Button>
            </div>
          </div>
        )}
      </DialogOverlay>

      <ConfirmDialog open={!!deleteTarget} onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        title="Revoke API Key" description={`Revoke "${deleteTarget?.name}"? Applications using this key will lose access immediately.`}
        confirmLabel="Revoke" variant="destructive" />
    </motion.div>
  );
}
