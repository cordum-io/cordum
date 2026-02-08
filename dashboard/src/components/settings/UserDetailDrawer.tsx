import { useState } from "react";
import { X, KeyRound, Shield } from "lucide-react";
import { Drawer } from "../ui/Drawer";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Select } from "../ui/Select";
import { useApiKeys, useUpdateUser } from "../../hooks/useSettings";
import { useConfigStore } from "../../state/config";
import type { User } from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const ROLES = ["Admin", "Operator", "Viewer", "Approver"] as const;

const ROLE_DESCRIPTIONS: Record<string, string> = {
  Admin: "Full access to all resources and settings.",
  Operator: "Can manage jobs, workflows, DLQ, and packs. Read-only for policies and settings.",
  Approver: "Can approve/reject jobs. Read-only for other resources.",
  Viewer: "Read-only access to all resources except settings.",
};

function roleBadgeVariant(role: string): "success" | "warning" | "info" | "default" {
  switch (role) {
    case "Admin": return "success";
    case "Operator": return "info";
    case "Approver": return "warning";
    default: return "default";
  }
}

function timeAgo(iso?: string): string {
  if (!iso) return "\u2014";
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1_000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

function formatDate(iso?: string): string {
  if (!iso) return "\u2014";
  return new Date(iso).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

// ---------------------------------------------------------------------------
// UserDetailDrawer
// ---------------------------------------------------------------------------

interface UserDetailDrawerProps {
  user: User | null;
  open: boolean;
  onClose: () => void;
  onChangePassword: (user: User) => void;
}

export function UserDetailDrawer({
  user,
  open,
  onClose,
  onChangePassword,
}: UserDetailDrawerProps) {
  const currentUserId = useConfigStore((s) => s.user?.id);
  const currentUsername = useConfigStore((s) => s.user?.username);
  const updateUser = useUpdateUser();
  const { data: keysData } = useApiKeys();

  const isSelf = user
    ? user.id === currentUserId || user.username === currentUsername
    : false;

  const primaryRole = user?.roles[0] ?? "Viewer";

  // Filter API keys associated with this user (heuristic: key name contains username)
  const associatedKeys = (keysData?.items ?? []).filter(
    (k) => user && k.name.toLowerCase().includes(user.username.toLowerCase()),
  );

  const handleRoleChange = (newRole: string) => {
    if (!user || isSelf) return;
    updateUser.mutate({ id: user.id, data: { roles: [newRole] } });
  };

  return (
    <Drawer open={open} onClose={onClose} size="md">
      {user && (
        <div className="space-y-6">
          {/* Header */}
          <div className="flex items-start justify-between">
            <div className="flex items-center gap-3">
              <div className="flex h-12 w-12 items-center justify-center rounded-full bg-accent/10 text-lg font-bold text-accent">
                {user.username[0]?.toUpperCase() ?? "?"}
              </div>
              <div>
                <h2 className="font-display text-lg font-semibold text-ink">
                  {user.display_name || user.username}
                </h2>
                <p className="text-xs text-muted">
                  @{user.username}
                  {user.email && <> &middot; {user.email}</>}
                </p>
              </div>
            </div>
            <button
              type="button"
              onClick={onClose}
              className="rounded-full p-1.5 hover:bg-surface2"
            >
              <X className="h-4 w-4 text-muted" />
            </button>
          </div>

          {/* Role section */}
          <div className="space-y-2">
            <p className="text-xs font-semibold uppercase tracking-wide text-muted">
              Role
            </p>
            <div className="flex items-center gap-3">
              {isSelf ? (
                <div className="flex items-center gap-2">
                  <Badge variant={roleBadgeVariant(primaryRole)}>
                    {primaryRole}
                  </Badge>
                  <span className="text-xs text-muted">(cannot modify own role)</span>
                </div>
              ) : (
                <Select
                  value={primaryRole}
                  onChange={(e) => handleRoleChange(e.target.value)}
                  className="w-36"
                  disabled={updateUser.isPending}
                >
                  {ROLES.map((r) => (
                    <option key={r} value={r}>{r}</option>
                  ))}
                </Select>
              )}
            </div>
            <p className="text-xs text-muted">
              <Shield className="mr-1 inline h-3 w-3" />
              {ROLE_DESCRIPTIONS[primaryRole] ?? "Standard access."}
            </p>
          </div>

          {/* Account info */}
          <div className="space-y-2">
            <p className="text-xs font-semibold uppercase tracking-wide text-muted">
              Account
            </p>
            <div className="grid grid-cols-2 gap-2 text-xs">
              <div>
                <span className="text-muted">Created</span>
                <p className="font-medium text-ink">{formatDate(user.createdAt)}</p>
              </div>
              <div>
                <span className="text-muted">Last login</span>
                <p className="font-medium text-ink">{timeAgo(user.lastLogin)}</p>
              </div>
              <div>
                <span className="text-muted">Tenant</span>
                <p className="font-medium text-ink">{user.tenant || "\u2014"}</p>
              </div>
              <div>
                <span className="text-muted">User ID</span>
                <p className="font-mono font-medium text-ink">{user.id.slice(0, 12)}</p>
              </div>
            </div>
          </div>

          {/* Associated API keys */}
          <div className="space-y-2">
            <p className="text-xs font-semibold uppercase tracking-wide text-muted">
              Associated API Keys
            </p>
            {associatedKeys.length === 0 ? (
              <p className="text-xs text-muted">No API keys associated with this user.</p>
            ) : (
              <div className="space-y-1.5">
                {associatedKeys.map((key) => (
                  <div
                    key={key.id}
                    className="flex items-center justify-between rounded-lg border border-border px-3 py-2"
                  >
                    <div>
                      <p className="text-xs font-medium text-ink">{key.name}</p>
                      <p className="font-mono text-[10px] text-muted">{key.prefix}...</p>
                    </div>
                    <div className="text-right text-[10px] text-muted">
                      <p>Used {timeAgo(key.lastUsed)}</p>
                      <p>{key.usageCount} calls</p>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Quick actions */}
          <div className="flex gap-2 border-t border-border pt-4">
            <Button
              variant="outline"
              size="sm"
              onClick={() => onChangePassword(user)}
            >
              <KeyRound className="mr-1.5 h-3.5 w-3.5" />
              Change Password
            </Button>
          </div>
        </div>
      )}
    </Drawer>
  );
}
