import { useState, useMemo } from "react";
import { useForm, Controller } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import {
  Key,
  Plus,
  Copy,
  Check,
  Trash2,
  RotateCw,
  Eye,
  Pencil,
  Shield,
  AlertTriangle,
  Clock,
} from "lucide-react";
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
import type { ApiKey } from "../api/types";
import { usePageTitle } from "../hooks/usePageTitle";

// ---------------------------------------------------------------------------
// Constants
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

const SCOPE_VARIANT: Record<string, "info" | "warning" | "danger"> = {
  "jobs:read": "info",
  "jobs:write": "info",
  "workflows:read": "info",
  "workflows:write": "info",
  "policy:read": "warning",
  "policy:write": "warning",
  admin: "danger",
};

function scopeIcon(scope: string) {
  if (scope === "admin") return Shield;
  if (scope.endsWith(":write")) return Pencil;
  return Eye;
}

const STALE_THRESHOLD_MS = 30 * 24 * 60 * 60_000; // 30 days

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function isStale(key: ApiKey): boolean {
  if (!key.lastUsed) return true; // never used
  return Date.now() - new Date(key.lastUsed).getTime() > STALE_THRESHOLD_MS;
}

function expiryStatus(key: ApiKey): "ok" | "soon" | "imminent" | "expired" | null {
  if (!key.expiresAt) return null;
  const remaining = new Date(key.expiresAt).getTime() - Date.now();
  if (remaining <= 0) return "expired";
  if (remaining < 24 * 60 * 60_000) return "imminent";
  if (remaining < 7 * 24 * 60 * 60_000) return "soon";
  return "ok";
}

function formatExpiry(key: ApiKey): string {
  if (!key.expiresAt) return "";
  const remaining = new Date(key.expiresAt).getTime() - Date.now();
  if (remaining <= 0) return "Expired";
  const days = Math.floor(remaining / (24 * 60 * 60_000));
  if (days > 0) return `${days}d`;
  const hours = Math.floor(remaining / (60 * 60_000));
  return `${hours}h`;
}

function fmtDate(d?: string): string {
  if (!d) return "\u2014";
  return new Date(d).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function tomorrowISO(): string {
  const d = new Date();
  d.setDate(d.getDate() + 1);
  return d.toISOString().slice(0, 10);
}

// ---------------------------------------------------------------------------
// Scope badge
// ---------------------------------------------------------------------------

function ScopeBadge({ scope }: { scope: string }) {
  const variant = SCOPE_VARIANT[scope] ?? "info";
  const Icon = scopeIcon(scope);
  return (
    <Badge variant={variant} className="text-[10px] gap-1">
      <Icon className="h-2.5 w-2.5" />
      {scope}
    </Badge>
  );
}

// ---------------------------------------------------------------------------
// Expiry badge
// ---------------------------------------------------------------------------

function ExpiryBadge({ apiKey }: { apiKey: ApiKey }) {
  const status = expiryStatus(apiKey);
  if (!status) return null;
  const text = formatExpiry(apiKey);
  if (status === "expired") return <Badge variant="danger">Expired</Badge>;
  if (status === "imminent")
    return (
      <Badge variant="danger" className="animate-pulse">
        <Clock className="mr-0.5 h-2.5 w-2.5" />
        {text}
      </Badge>
    );
  if (status === "soon")
    return (
      <Badge variant="warning">
        <Clock className="mr-0.5 h-2.5 w-2.5" />
        {text}
      </Badge>
    );
  return (
    <Badge variant="success">
      <Clock className="mr-0.5 h-2.5 w-2.5" />
      {text}
    </Badge>
  );
}

// ---------------------------------------------------------------------------
// Create key form schema
// ---------------------------------------------------------------------------

const createKeySchema = z.object({
  name: z.string().min(1, "Name is required").max(64),
  scopes: z.array(z.string()).min(1, "Select at least one scope"),
  expiresAt: z.string().optional(),
});

type CreateKeyFormValues = z.infer<typeof createKeySchema>;

// ---------------------------------------------------------------------------
// Create Key Form
// ---------------------------------------------------------------------------

function CreateKeyForm({
  onClose,
  defaultName,
  defaultScopes,
}: {
  onClose: () => void;
  defaultName?: string;
  defaultScopes?: string[];
}) {
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
    defaultValues: {
      name: defaultName ?? "",
      scopes: defaultScopes ?? [],
      expiresAt: "",
    },
  });

  const onSubmit = (values: CreateKeyFormValues) => {
    createKey.mutate(
      {
        name: values.name,
        scopes: values.scopes,
        expiresAt: values.expiresAt || undefined,
      },
      {
        onSuccess: (res) => {
          setNewSecret(res.secret);
        },
      },
    );
  };

  const copySecret = async () => {
    if (!newSecret) return;
    await navigator.clipboard.writeText(newSecret);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

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
          {defaultName ? "Rotate API Key" : "Create API Key"}
        </h3>

        <div className="space-y-1">
          <label className="text-xs font-semibold text-muted">Name</label>
          <Input {...register("name")} placeholder="e.g. CI Pipeline Key" />
          {errors.name && (
            <p className="text-xs text-danger">{errors.name.message}</p>
          )}
        </div>

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

        <div className="space-y-1">
          <label className="text-xs font-semibold text-muted">
            Expires (optional)
          </label>
          <Input type="date" {...register("expiresAt")} min={tomorrowISO()} />
        </div>

        {createKey.isError && (
          <p className="text-xs text-danger">
            Failed to create key: {createKey.error.message}
          </p>
        )}

        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" type="button" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            size="sm"
            type="submit"
            disabled={createKey.isPending}
          >
            {createKey.isPending
              ? "Creating..."
              : defaultName
                ? "Create Rotated Key"
                : "Create Key"}
          </Button>
        </div>
      </form>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Rotate Key Modal (inline card)
// ---------------------------------------------------------------------------

function RotateKeyCard({
  apiKey,
  onClose,
  onRevokeOld,
}: {
  apiKey: ApiKey;
  onClose: () => void;
  onRevokeOld: (id: string) => void;
}) {
  const [step, setStep] = useState<"create" | "done">("create");

  if (step === "create") {
    return (
      <div className="space-y-3">
        <Card className="border-accent/30 bg-accent/5">
          <div className="space-y-2">
            <p className="text-sm font-semibold text-ink">
              Rotating key: <span className="text-accent">{apiKey.name}</span>
            </p>
            <p className="text-xs text-muted">
              A new key will be created with the same scopes. After copying the
              new secret, you can revoke the old key.
            </p>
            <div className="flex flex-wrap gap-1">
              {apiKey.scopes.map((s) => (
                <ScopeBadge key={s} scope={s} />
              ))}
            </div>
          </div>
        </Card>
        <CreateKeyForm
          onClose={() => {
            setStep("done");
          }}
          defaultName={`${apiKey.name} (rotated)`}
          defaultScopes={apiKey.scopes}
        />
      </div>
    );
  }

  return (
    <Card>
      <div className="space-y-4">
        <h3 className="font-display text-lg font-semibold text-ink">
          Rotation Complete
        </h3>
        <p className="text-sm text-muted">
          New key created. Would you like to revoke the old key now?
        </p>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={onClose}>
            Keep old key
          </Button>
          <Button
            variant="danger"
            size="sm"
            onClick={() => {
              onRevokeOld(apiKey.id);
              onClose();
            }}
          >
            <Trash2 className="h-3.5 w-3.5" />
            Revoke old key now
          </Button>
        </div>
      </div>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// SettingsKeysPage
// ---------------------------------------------------------------------------

export default function SettingsKeysPage() {
  usePageTitle("Settings - API Keys");
  const { data, isLoading } = useApiKeys();
  const revokeKey = useRevokeApiKey();
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [revokeId, setRevokeId] = useState<string | null>(null);
  const [rotatingKey, setRotatingKey] = useState<ApiKey | null>(null);

  const keys = data?.items ?? [];

  const staleCount = useMemo(
    () => keys.filter((k) => isStale(k)).length,
    [keys],
  );

  return (
    <div className="space-y-4">
      {/* Header row */}
      <div className="flex items-center justify-between">
        <div className="space-y-1">
          <p className="text-sm text-muted">
            Manage API keys for programmatic access.
          </p>
          {staleCount > 0 && (
            <p className="flex items-center gap-1 text-xs text-warning">
              <AlertTriangle className="h-3 w-3" />
              {staleCount} key{staleCount !== 1 ? "s" : ""} unused for 30+ days
            </p>
          )}
        </div>
        <Button
          variant="primary"
          size="sm"
          type="button"
          onClick={() => {
            setShowCreateForm(true);
            setRotatingKey(null);
          }}
        >
          <Plus className="h-3.5 w-3.5" />
          Create Key
        </Button>
      </div>

      {/* Create form (inline) */}
      {showCreateForm && !rotatingKey && (
        <CreateKeyForm onClose={() => setShowCreateForm(false)} />
      )}

      {/* Rotate form (inline) */}
      {rotatingKey && (
        <RotateKeyCard
          apiKey={rotatingKey}
          onClose={() => setRotatingKey(null)}
          onRevokeOld={(id) => revokeKey.mutate(id)}
        />
      )}

      {/* Table */}
      {isLoading ? (
        <div className="space-y-3">
          {Array.from({ length: 3 }, (_, i) => (
            <div key={i} className="h-16 animate-pulse rounded-2xl bg-surface2" />
          ))}
        </div>
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
              {keys.map((k) => {
                const stale = isStale(k);
                return (
                  <tr
                    key={k.id}
                    className={cn(
                      "transition hover:bg-surface2/30",
                      stale && "border-l-4 border-l-warning",
                    )}
                  >
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-2">
                        <span className="font-medium text-ink">{k.name}</span>
                        {stale && (
                          <Badge variant="warning" className="text-[10px]">
                            Unused 30d+
                          </Badge>
                        )}
                        <ExpiryBadge apiKey={k} />
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <code className="rounded bg-surface2 px-2 py-0.5 text-xs font-mono text-muted">
                        ****{k.prefix}
                      </code>
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex flex-wrap gap-1">
                        {k.scopes.map((s) => (
                          <ScopeBadge key={s} scope={s} />
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
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="outline"
                          size="sm"
                          type="button"
                          onClick={() => {
                            setRotatingKey(k);
                            setShowCreateForm(false);
                          }}
                        >
                          <RotateCw className="h-3.5 w-3.5" />
                          Rotate
                        </Button>
                        <Button
                          variant="danger"
                          size="sm"
                          type="button"
                          onClick={() => setRevokeId(k.id)}
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                          Revoke
                        </Button>
                      </div>
                    </td>
                  </tr>
                );
              })}
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
