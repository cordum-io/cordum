import { useState } from "react";
import { useForm, Controller } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Activity, Users, Key, Plus, Copy, Check, Trash2 } from "lucide-react";
import { SystemHealthTab } from "../components/settings/SystemHealthTab";
import { UsersTab } from "../components/settings/UsersTab";
import {
  useApiKeys,
  useCreateApiKey,
  useRevokeApiKey,
} from "../hooks/useSettings";
import { Card } from "../components/ui/Card";
import { Badge } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { Input } from "../components/ui/Input";
import { ConfirmDialog } from "../components/ui/ConfirmDialog";
import { cn } from "../lib/utils";

// ---------------------------------------------------------------------------
// Tab definitions — API Keys is FIRST
// ---------------------------------------------------------------------------

const TABS = [
  { id: "keys", label: "API Keys", icon: Key },
  { id: "users", label: "Users", icon: Users },
  { id: "health", label: "System Health", icon: Activity },
] as const;

type TabId = (typeof TABS)[number]["id"];

// ---------------------------------------------------------------------------
// Available scopes
// ---------------------------------------------------------------------------

const AVAILABLE_SCOPES = [
  { value: "jobs:read", label: "Jobs Read" },
  { value: "jobs:write", label: "Jobs Write" },
  { value: "workflows:read", label: "Workflows Read" },
  { value: "workflows:write", label: "Workflows Write" },
  { value: "policy:read", label: "Policy Read" },
  { value: "policy:write", label: "Policy Write" },
  { value: "admin", label: "Admin" },
] as const;

// ---------------------------------------------------------------------------
// Create key form schema (React Hook Form + Zod)
// ---------------------------------------------------------------------------

const createKeySchema = z.object({
  name: z.string().min(1, "Name is required").max(64),
  scopes: z.array(z.string()).min(1, "Select at least one scope"),
});

type CreateKeyFormValues = z.infer<typeof createKeySchema>;

// ---------------------------------------------------------------------------
// Create Key Form
// ---------------------------------------------------------------------------

function CreateKeyForm({ onClose }: { onClose: () => void }) {
  const createKey = useCreateApiKey();
  const [newSecret, setNewSecret] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const {
    register,
    handleSubmit,
    control,
    formState: { errors },
  } = useForm<CreateKeyFormValues>({
    resolver: zodResolver(createKeySchema),
    defaultValues: { name: "", scopes: [] },
  });

  const onSubmit = (values: CreateKeyFormValues) => {
    createKey.mutate(values, {
      onSuccess: (res) => {
        setNewSecret(res.secret);
      },
    });
  };

  const copySecret = async () => {
    if (!newSecret) return;
    await navigator.clipboard.writeText(newSecret);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  // After creation — show the key ONCE
  if (newSecret) {
    return (
      <Card>
        <div className="space-y-4">
          <div className="flex items-center gap-2">
            <Key className="h-5 w-5 text-accent" />
            <h3 className="font-display text-lg font-semibold text-ink">
              API Key Created
            </h3>
          </div>
          <p className="text-sm text-danger font-semibold">
            Copy this key now. It will not be shown again.
          </p>
          <div className="flex items-center gap-2 rounded-xl border border-border bg-surface2 px-4 py-3">
            <code className="flex-1 break-all text-xs font-mono text-ink">
              {newSecret}
            </code>
            <button
              type="button"
              onClick={copySecret}
              className="shrink-0 rounded-lg p-1.5 hover:bg-white/60 transition"
            >
              {copied ? (
                <Check className="h-4 w-4 text-success" />
              ) : (
                <Copy className="h-4 w-4 text-muted" />
              )}
            </button>
          </div>
          <div className="flex justify-end">
            <Button variant="outline" size="sm" type="button" onClick={onClose}>
              Done
            </Button>
          </div>
        </div>
      </Card>
    );
  }

  return (
    <Card>
      <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
        <h3 className="font-display text-lg font-semibold text-ink">
          Create API Key
        </h3>

        {/* Name */}
        <div className="space-y-1">
          <label className="text-xs font-semibold text-muted">Name</label>
          <Input
            {...register("name")}
            placeholder="e.g. CI Pipeline Key"
          />
          {errors.name && (
            <p className="text-xs text-danger">{errors.name.message}</p>
          )}
        </div>

        {/* Scopes */}
        <div className="space-y-2">
          <label className="text-xs font-semibold text-muted">Scopes</label>
          <Controller
            name="scopes"
            control={control}
            render={({ field }) => (
              <div className="flex flex-wrap gap-2">
                {AVAILABLE_SCOPES.map((scope) => {
                  const checked = field.value.includes(scope.value);
                  return (
                    <label
                      key={scope.value}
                      className={cn(
                        "flex cursor-pointer items-center gap-2 rounded-xl border px-3 py-2 text-xs font-medium transition",
                        checked
                          ? "border-accent bg-accent/10 text-accent"
                          : "border-border text-muted hover:border-accent/40",
                      )}
                    >
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={(e) => {
                          if (e.target.checked) {
                            field.onChange([...field.value, scope.value]);
                          } else {
                            field.onChange(
                              field.value.filter((s) => s !== scope.value),
                            );
                          }
                        }}
                        className="sr-only"
                      />
                      {scope.label}
                    </label>
                  );
                })}
              </div>
            )}
          />
          {errors.scopes && (
            <p className="text-xs text-danger">{errors.scopes.message}</p>
          )}
        </div>

        {/* Error */}
        {createKey.isError && (
          <p className="text-xs text-danger">
            Failed to create key: {createKey.error.message}
          </p>
        )}

        {/* Actions */}
        <div className="flex justify-end gap-2">
          <Button
            variant="ghost"
            size="sm"
            type="button"
            onClick={onClose}
          >
            Cancel
          </Button>
          <Button
            variant="primary"
            size="sm"
            type="submit"
            disabled={createKey.isPending}
          >
            {createKey.isPending ? "Creating..." : "Create Key"}
          </Button>
        </div>
      </form>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// API Keys Tab
// ---------------------------------------------------------------------------

function ApiKeysTab() {
  const { data, isLoading } = useApiKeys();
  const revokeKey = useRevokeApiKey();
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [revokeId, setRevokeId] = useState<string | null>(null);

  const keys = data?.items ?? [];

  const fmtDate = (d?: string) => {
    if (!d) return "\u2014";
    return new Date(d).toLocaleDateString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
    });
  };

  return (
    <div className="space-y-4">
      {/* Header row */}
      <div className="flex items-center justify-between">
        <p className="text-sm text-muted">
          Manage API keys for programmatic access.
        </p>
        <Button
          variant="primary"
          size="sm"
          type="button"
          onClick={() => setShowCreateForm(true)}
        >
          <Plus className="h-3.5 w-3.5" />
          Create Key
        </Button>
      </div>

      {/* Create form (inline) */}
      {showCreateForm && (
        <CreateKeyForm onClose={() => setShowCreateForm(false)} />
      )}

      {/* Table */}
      {isLoading ? (
        <p className="py-8 text-center text-sm text-muted">Loading keys...</p>
      ) : keys.length === 0 ? (
        <Card>
          <p className="py-8 text-center text-sm text-muted">
            No API keys yet. Create one to get started.
          </p>
        </Card>
      ) : (
        <div className="overflow-x-auto rounded-2xl border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-surface2/60 text-left text-xs uppercase tracking-wider text-muted">
                <th className="px-4 py-3">Name</th>
                <th className="px-4 py-3">Key Prefix</th>
                <th className="px-4 py-3">Scopes</th>
                <th className="px-4 py-3">Created</th>
                <th className="px-4 py-3">Last Used</th>
                <th className="px-4 py-3 text-right">Usage</th>
                <th className="px-4 py-3" />
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {keys.map((k) => (
                <tr key={k.id} className="hover:bg-surface2/30 transition">
                  <td className="px-4 py-3 font-medium text-ink">{k.name}</td>
                  <td className="px-4 py-3">
                    <code className="rounded bg-surface2 px-2 py-0.5 text-xs font-mono text-muted">
                      ****{k.prefix}
                    </code>
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex flex-wrap gap-1">
                      {k.scopes.map((s) => (
                        <Badge key={s} variant="info" className="text-[10px]">
                          {s}
                        </Badge>
                      ))}
                    </div>
                  </td>
                  <td className="px-4 py-3 text-xs text-muted">
                    {fmtDate(k.createdAt)}
                  </td>
                  <td className="px-4 py-3 text-xs text-muted">
                    {fmtDate(k.lastUsed)}
                  </td>
                  <td className="px-4 py-3 text-right text-xs font-mono text-ink">
                    {k.usageCount.toLocaleString()}
                  </td>
                  <td className="px-4 py-3 text-right">
                    <Button
                      variant="danger"
                      size="sm"
                      type="button"
                      onClick={() => setRevokeId(k.id)}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                      Revoke
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Revoke confirmation dialog */}
      <ConfirmDialog
        open={revokeId !== null}
        title="Revoke API Key"
        message="This key will be permanently revoked. Any integrations using it will stop working immediately. This action cannot be undone."
        confirmLabel="Revoke Key"
        confirmVariant="danger"
        isPending={revokeKey.isPending}
        onConfirm={() => {
          if (revokeId) {
            revokeKey.mutate(revokeId, {
              onSuccess: () => setRevokeId(null),
            });
          }
        }}
        onCancel={() => setRevokeId(null)}
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// SettingsPage
// ---------------------------------------------------------------------------

export default function SettingsPage() {
  const [activeTab, setActiveTab] = useState<TabId>("keys");

  return (
    <div className="space-y-6">
      <div>
        <h1 className="font-display text-2xl font-bold text-ink">Settings</h1>
        <p className="text-sm text-muted">
          API keys, users & RBAC, and system health.
        </p>
      </div>

      {/* Tab bar */}
      <div className="flex items-center gap-1">
        {TABS.map((tab) => {
          const Icon = tab.icon;
          return (
            <button
              key={tab.id}
              type="button"
              onClick={() => setActiveTab(tab.id)}
              className={cn(
                "flex items-center gap-1.5 rounded-full px-3 py-1.5 text-xs font-semibold transition",
                activeTab === tab.id
                  ? "bg-accent/15 text-accent"
                  : "text-muted hover:bg-surface2",
              )}
            >
              <Icon className="h-3.5 w-3.5" />
              {tab.label}
            </button>
          );
        })}
      </div>

      {/* Tab content */}
      {activeTab === "keys" && <ApiKeysTab />}
      {activeTab === "users" && <UsersTab />}
      {activeTab === "health" && <SystemHealthTab />}
    </div>
  );
}
