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
import { Checkbox } from "@/components/ui/Checkbox";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { Input } from "@/components/ui/Input";
import { LabeledField } from "@/components/ui/LabeledField";
import { SkeletonTable } from "@/components/ui/Skeleton";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { DialogOverlay } from "@/components/ui/DialogOverlay";
import { Key, Plus, Copy, Trash2, X } from "lucide-react";
import { formatRelativeTime } from "@/lib/utils";
import { toast } from "sonner";
import { friendlyError } from "@/lib/friendlyError";
import { ErrorBanner } from "@/components/ui/ErrorBanner";

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
  const f = friendlyError(err, "create API key");
  toast.error(f.title, { description: f.description });
}

export function handleDeleteKeySuccess(deps: DeleteKeyMutationDeps) {
  deps.queryClient.invalidateQueries({ queryKey: ["api-keys"] });
  toast.success("API key revoked");
  deps.setDeleteTarget(null);
}

export function handleDeleteKeyError(err: unknown) {
  const f = friendlyError(err, "revoke API key");
  toast.error(f.title, { description: f.description });
}

export default function SettingsKeysPage() {
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [newKeyName, setNewKeyName] = useState("");
  const [newKeyScopes, setNewKeyScopes] = useState<string[]>(["read"]);
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ApiKey | null>(null);

  const { data: keys, isLoading, isError, error, refetch } = useQuery({
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

  if (isError) {
    return <ErrorBanner message={error instanceof Error ? error.message : "Failed to load API keys"} onRetry={() => void refetch()} />;
  }

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
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Name</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Key</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Scopes</th>
                <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Last Used</th>
                <th className="text-right px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Actions</th>
              </tr>
            </thead>
            <tbody>
              {keys.map((key, i) => (
                <motion.tr key={key.id} initial={{ opacity: 0 }} animate={{ opacity: 1 }} transition={{ delay: i * 0.03 }}
                  className="border-b border-border last:border-0 hover:bg-surface-1 transition-colors">
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-2">
                      <Key className="w-3.5 h-3.5 text-cordum" />
                      <span className="text-sm font-medium text-foreground">{key.name}</span>
                    </div>
                  </td>
                  <td className="px-5 py-3 font-mono text-xs text-muted-foreground">{key.prefix}...****</td>
                  <td className="px-5 py-3">
                    <div className="flex gap-1">{key.scopes.map(s => <StatusBadge key={s} variant="info">{s}</StatusBadge>)}</div>
                  </td>
                  <td className="px-5 py-3 text-xs text-muted-foreground">{key.lastUsed ? formatRelativeTime(key.lastUsed) : "Never"}</td>
                  <td className="px-5 py-3 text-right">
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 text-destructive hover:bg-destructive/10"
                      onClick={() => setDeleteTarget(key)}
                      aria-label={`Revoke ${key.name}`}
                    >
                      <Trash2 className="w-3.5 h-3.5 text-destructive" />
                    </Button>
                  </td>
                </motion.tr>
              ))}
            </tbody>
          </table>
          </div>
        </div>
      )}

      {/* Create Dialog */}
      <DialogOverlay
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        label={createdKey ? "Key Created" : "Create API key"}
        className="w-[440px] rounded-3xl border border-border bg-surface-1 p-6 shadow-2xl"
      >
        <div className="mb-4 flex items-center justify-between gap-4">
          <div>
            <h2 className="text-sm font-display font-semibold text-foreground">{createdKey ? "Key Created" : "Create API Key"}</h2>
            <p className="mt-1 text-xs text-muted-foreground">
              Generate a scoped credential for CI pipelines, automation, or local tooling.
            </p>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="h-8 w-8"
            onClick={() => setCreateOpen(false)}
            aria-label="Close create key dialog"
          >
            <X className="w-4 h-4 text-muted-foreground" />
          </Button>
        </div>
        {createdKey ? (
          <div className="space-y-4">
            <InfoBanner variant="warning" title="Copy this key now">
              It will not be shown again after you close this dialog.
            </InfoBanner>
            <LabeledField
              label="New secret"
              description="Store it in your secrets manager before leaving this screen."
              action={(
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    navigator.clipboard.writeText(createdKey);
                    toast.success("Copied");
                  }}
                >
                  <Copy className="w-3.5 h-3.5" />
                  Copy
                </Button>
              )}
            >
              <code className="block rounded-2xl border border-border bg-surface-2 px-3 py-2 text-xs font-mono text-foreground break-all">
                {createdKey}
              </code>
            </LabeledField>
            <Button variant="primary" size="sm" className="w-full" onClick={() => setCreateOpen(false)}>Done</Button>
          </div>
        ) : (
          <div className="space-y-4">
            <LabeledField
              label="Name"
              description="Use a human-readable label so operators can tell what the key is for."
            >
              <Input
                type="text"
                value={newKeyName}
                onChange={(e) => setNewKeyName(e.target.value)}
                placeholder="e.g., CI Pipeline"
              />
            </LabeledField>
            <LabeledField
              label="Scopes"
              description="Grant only the permissions the integration actually needs."
            >
              <div className="grid gap-2 sm:grid-cols-3">
                {SCOPES.map(s => (
                  <Checkbox
                    key={s}
                    checked={newKeyScopes.includes(s)}
                    onChange={() =>
                      setNewKeyScopes(prev =>
                        prev.includes(s) ? prev.filter(x => x !== s) : [...prev, s],
                      )
                    }
                    label={<span className="capitalize">{s}</span>}
                    description={s === "admin" ? "Full administrative access" : s === "write" ? "Create and mutate resources" : "Read-only access"}
                  />
                ))}
              </div>
            </LabeledField>
            <div className="flex justify-end gap-2 pt-2">
              <Button variant="ghost" size="sm" onClick={() => setCreateOpen(false)}>Cancel</Button>
              <Button
                variant="primary"
                size="sm"
                onClick={() => createMutation.mutate()}
                loading={createMutation.isPending}
                disabled={!newKeyName.trim() || newKeyScopes.length === 0}
              >
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
