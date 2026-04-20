import { useCallback, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Trash2, KeyRound, X } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Input } from "../ui/Input";
import { Select } from "../ui/Select";
import {
  useUsers,
  useCreateUser,
  useUpdateUser,
  useDeleteUser,
  useResetUserPassword,
} from "../../hooks/useSettings";
import {
  PasswordStrengthIndicator,
  PASSWORD_REQUIREMENTS_TEXT,
} from "./ChangePasswordSection";
import { useConfigStore } from "../../state/config";
import { useToastStore } from "../../state/toast";
import { cn } from "../../lib/utils";
import type { User } from "../../api/types";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

export const ROLES = ["Admin", "Operator", "Viewer", "Approver"] as const;

export function roleBadgeVariant(
  role: string,
): "success" | "warning" | "info" | "default" {
  switch (role) {
    case "Admin":
      return "success";
    case "Operator":
      return "info";
    case "Approver":
      return "warning";
    default:
      return "default";
  }
}

export function timeAgo(iso?: string): string {
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

// ---------------------------------------------------------------------------
// Create user form
// ---------------------------------------------------------------------------

export const createUserSchema = z.object({
  username: z.string().min(3, "Username must be at least 3 characters"),
  password: z
    .string()
    .min(12, "Password must be at least 12 characters")
    .regex(/[A-Z]/, "Must include an uppercase letter")
    .regex(/[0-9]/, "Must include a digit")
    .regex(/[^a-zA-Z0-9]/, "Must include a special character"),
  role: z.string().min(1, "Role is required"),
});

type CreateUserForm = z.infer<typeof createUserSchema>;

// ---------------------------------------------------------------------------
// Change password form
// ---------------------------------------------------------------------------

const changePasswordSchema = z
  .object({
    password: z
      .string()
      .min(12, "Password must be at least 12 characters")
      .regex(/[a-z]/, "Must include a lowercase letter")
      .regex(/[A-Z]/, "Must include an uppercase letter")
      .regex(/[0-9]/, "Must include a number")
      .regex(/[^a-zA-Z0-9]/, "Must include a special character"),
    confirm: z.string(),
  })
  .refine((d) => d.password === d.confirm, {
    message: "Passwords do not match",
    path: ["confirm"],
  });

type ChangePasswordForm = z.infer<typeof changePasswordSchema>;

// ---------------------------------------------------------------------------
// Create user modal
// ---------------------------------------------------------------------------

function CreateUserModal({
  onClose,
}: {
  onClose: () => void;
}) {
  const createUser = useCreateUser();

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<CreateUserForm>({
    resolver: zodResolver(createUserSchema),
    defaultValues: { username: "", password: "", role: "Viewer" },
  });

  function onSubmit(data: CreateUserForm) {
    createUser.mutate(data, { onSuccess: onClose });
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="surface-card w-full max-w-md rounded-3xl p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h3 className="font-display text-lg font-semibold text-ink">
            Create User
          </h3>
          <button type="button" onClick={onClose} className="rounded-full p-1 hover:bg-surface2">
            <X className="h-4 w-4 text-muted-foreground" />
          </button>
        </div>

        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
          <div>
            <label className="mb-1 block text-xs font-semibold text-muted-foreground">
              Username
            </label>
            <Input placeholder="e.g. jane.doe" {...register("username")} />
            {errors.username && (
              <p className="mt-1 text-xs text-danger">{errors.username.message}</p>
            )}
          </div>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted-foreground">
              Password
            </label>
            <Input type="password" placeholder="Min 12 characters" {...register("password")} />
            <p className="mt-1 text-xs text-muted-foreground">Min 12 chars, 1 uppercase, 1 digit, 1 special character</p>
            {errors.password && (
              <p className="mt-1 text-xs text-danger">{errors.password.message}</p>
            )}
          </div>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted-foreground">
              Role
            </label>
            <Select {...register("role")}>
              {ROLES.map((r) => (
                <option key={r} value={r}>
                  {r}
                </option>
              ))}
            </Select>
            {errors.role && (
              <p className="mt-1 text-xs text-danger">{errors.role.message}</p>
            )}
          </div>

          <div className="flex justify-end gap-3">
            <Button variant="ghost" size="sm" type="button" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={createUser.isPending}>
              {createUser.isPending ? "Creating..." : "Create User"}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Change password modal
// ---------------------------------------------------------------------------

function ChangePasswordModal({
  user,
  onClose,
}: {
  user: User;
  onClose: () => void;
}) {
  const resetUserPassword = useResetUserPassword();
  const [error, setError] = useState("");

  const {
    register,
    handleSubmit,
    watch,
    formState: { errors },
  } = useForm<ChangePasswordForm>({
    resolver: zodResolver(changePasswordSchema),
    defaultValues: { password: "", confirm: "" },
  });
  const password = watch("password");

  async function onSubmit(data: ChangePasswordForm) {
    setError("");
    try {
      await resetUserPassword.mutateAsync({
        userId: user.id,
        password: data.password,
      });
      useToastStore.getState().addToast({
        type: "success",
        title: `Password reset for ${user.username}`,
      });
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to reset password");
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="surface-card w-full max-w-md rounded-3xl p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h3 className="font-display text-lg font-semibold text-ink">
            Reset Password for {user.username}
          </h3>
          <button type="button" onClick={onClose} className="rounded-full p-1 hover:bg-surface2">
            <X className="h-4 w-4 text-muted-foreground" />
          </button>
        </div>

        {error && (
          <div className="mb-4 rounded-xl bg-[color:rgba(184,58,58,0.14)] px-4 py-2 text-sm text-danger">
            {error}
          </div>
        )}

        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
          <div>
            <label className="mb-1 block text-xs font-semibold text-muted-foreground">
              New Password
            </label>
            <Input type="password" placeholder="Min 12 characters" {...register("password")} />
            <p className="mt-1 text-xs text-muted-foreground">{PASSWORD_REQUIREMENTS_TEXT}</p>
            <div className="mt-2">
              <PasswordStrengthIndicator password={password ?? ""} />
            </div>
            {errors.password && (
              <p className="mt-1 text-xs text-danger">{errors.password.message}</p>
            )}
          </div>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted-foreground">
              Confirm Password
            </label>
            <Input type="password" placeholder="Repeat password" {...register("confirm")} />
            {errors.confirm && (
              <p className="mt-1 text-xs text-danger">{errors.confirm.message}</p>
            )}
          </div>

          <div className="flex justify-end gap-3">
            <Button variant="ghost" size="sm" type="button" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={resetUserPassword.isPending}>
              {resetUserPassword.isPending ? "Resetting..." : "Reset Password"}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Confirm delete dialog
// ---------------------------------------------------------------------------

function ConfirmDelete({
  user,
  isPending,
  onConfirm,
  onCancel,
}: {
  user: User;
  isPending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="surface-card w-full max-w-sm rounded-3xl p-6 shadow-xl">
        <h3 className="mb-4 font-display text-lg font-semibold text-ink">
          Delete User
        </h3>
        <p className="mb-6 text-sm text-muted-foreground">
          Are you sure you want to delete{" "}
          <strong className="text-ink">{user.username}</strong>? This action
          cannot be undone.
        </p>
        <div className="flex justify-end gap-3">
          <Button variant="ghost" size="sm" onClick={onCancel} disabled={isPending}>
            Cancel
          </Button>
          <Button variant="danger" size="sm" onClick={onConfirm} disabled={isPending}>
            {isPending ? "Deleting..." : "Delete"}
          </Button>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Bulk confirm dialog
// ---------------------------------------------------------------------------

function BulkConfirmDialog({
  count,
  action,
  role,
  isPending,
  onConfirm,
  onCancel,
}: {
  count: number;
  action: "role" | "delete";
  role?: string;
  isPending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="surface-card w-full max-w-sm rounded-3xl p-6 shadow-xl">
        <h3 className="mb-4 font-display text-lg font-semibold text-ink">
          {action === "delete" ? "Delete Users" : "Change Role"}
        </h3>
        <p className="mb-6 text-sm text-muted-foreground">
          {action === "delete"
            ? `Are you sure you want to delete ${count} user${count !== 1 ? "s" : ""}? This action cannot be undone.`
            : `Change the role of ${count} user${count !== 1 ? "s" : ""} to ${role}?`}
        </p>
        <div className="flex justify-end gap-3">
          <Button variant="ghost" size="sm" onClick={onCancel} disabled={isPending}>
            Cancel
          </Button>
          <Button
            variant={action === "delete" ? "danger" : "default"}
            size="sm"
            onClick={onConfirm}
            disabled={isPending}
          >
            {isPending ? "Processing..." : "Confirm"}
          </Button>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Bulk action bar
// ---------------------------------------------------------------------------

function BulkActionBar({
  count,
  onChangeRole,
  onDelete,
  onClear,
}: {
  count: number;
  onChangeRole: (role: string) => void;
  onDelete: () => void;
  onClear: () => void;
}) {
  return (
    <div className="fixed bottom-6 left-1/2 z-30 -translate-x-1/2 flex items-center gap-3 rounded-2xl border border-border bg-surface px-5 py-3 shadow-xl">
      <span className="text-xs font-semibold text-ink">
        {count} selected
      </span>
      <Select
        className="h-7 w-32 text-xs"
        value=""
        onChange={(e) => {
          if (e.target.value) onChangeRole(e.target.value);
        }}
      >
        <option value="">Change role...</option>
        {ROLES.map((r) => (
          <option key={r} value={r}>{r}</option>
        ))}
      </Select>
      <Button variant="danger" size="sm" onClick={onDelete}>
        <Trash2 className="mr-1 h-3 w-3" />
        Delete
      </Button>
      <Button variant="ghost" size="sm" onClick={onClear}>
        Clear
      </Button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// UsersTab
// ---------------------------------------------------------------------------

export function UsersTab() {
  const { data, isLoading, isError } = useUsers();
  const updateUser = useUpdateUser();
  const deleteUser = useDeleteUser();

  const [showCreate, setShowCreate] = useState(false);
  const [passwordTarget, setPasswordTarget] = useState<User | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<User | null>(null);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());

  // Bulk action confirm state
  const [bulkConfirm, setBulkConfirm] = useState<{
    action: "role" | "delete";
    role?: string;
  } | null>(null);
  const [bulkPending, setBulkPending] = useState(false);

  const currentUserId = useConfigStore((s) => s.user?.id);
  const currentUsername = useConfigStore((s) => s.user?.username);
  const users = data?.items ?? [];

  const isSelf = useCallback(
    (user: User) => user.id === currentUserId || user.username === currentUsername,
    [currentUserId, currentUsername],
  );

  // Selectable users (exclude self)
  const selectableUsers = users.filter((u) => !isSelf(u));

  const toggleSelect = useCallback((id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  }, []);

  const toggleSelectAll = useCallback(() => {
    setSelectedIds((prev) =>
      prev.size === selectableUsers.length
        ? new Set()
        : new Set(selectableUsers.map((u) => u.id)),
    );
  }, [selectableUsers]);

  function handleRoleChange(user: User, newRole: string) {
    updateUser.mutate({ id: user.id, data: { roles: [newRole] } });
  }

  function handleDelete() {
    if (!deleteTarget) return;
    deleteUser.mutate(deleteTarget.id, {
      onSuccess: () => setDeleteTarget(null),
    });
  }

  // Bulk actions
  async function handleBulkConfirm() {
    if (!bulkConfirm) return;
    setBulkPending(true);
    const ids = [...selectedIds];
    if (bulkConfirm.action === "role" && bulkConfirm.role) {
      for (const id of ids) {
        await new Promise<void>((resolve) => {
          updateUser.mutate(
            { id, data: { roles: [bulkConfirm.role!] } },
            { onSettled: () => resolve() },
          );
        });
      }
    } else if (bulkConfirm.action === "delete") {
      for (const id of ids) {
        await new Promise<void>((resolve) => {
          deleteUser.mutate(id, { onSettled: () => resolve() });
        });
      }
    }
    setBulkPending(false);
    setBulkConfirm(null);
    setSelectedIds(new Set());
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="font-display text-lg font-semibold text-ink">
          Users &amp; RBAC
        </h2>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          Create User
        </Button>
      </div>

      <div className="surface-card overflow-hidden rounded-2xl">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="border-b border-border">
              <tr>
                <th className="w-10 px-3 py-3">
                  {selectableUsers.length > 0 && (
                    <input
                      type="checkbox"
                      checked={selectedIds.size > 0 && selectedIds.size === selectableUsers.length}
                      ref={(el) => {
                        if (el) el.indeterminate = selectedIds.size > 0 && selectedIds.size < selectableUsers.length;
                      }}
                      onChange={toggleSelectAll}
                      className="h-3.5 w-3.5 rounded border-border text-accent focus:ring-accent cursor-pointer"
                    />
                  )}
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                  Username
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                  Role
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                  Created
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                  Last Login
                </th>
                <th className="px-4 py-3" />
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {isLoading &&
                Array.from({ length: 3 }, (_, i) => (
                  <tr key={i} className="animate-pulse">
                    {Array.from({ length: 6 }, (_, j) => (
                      <td key={j} className="px-4 py-3">
                        <div className="h-4 rounded bg-surface2 w-3/4" />
                      </td>
                    ))}
                  </tr>
                ))}

              {!isLoading && isError && (
                <tr>
                  <td colSpan={6} className="px-4 py-12 text-center text-muted-foreground">
                    Failed to load users.
                  </td>
                </tr>
              )}

              {!isLoading && !isError && users.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-4 py-12 text-center text-muted-foreground">
                    No users yet. Create one to get started.
                  </td>
                </tr>
              )}

              {!isLoading &&
                users.map((user) => {
                  const primaryRole = user.roles[0] ?? "Viewer";
                  const self = isSelf(user);

                  return (
                    <tr
                      key={user.id}
                      className={cn(
                        "transition-colors hover:bg-surface2/60",
                        selectedIds.has(user.id) && "bg-accent/5",
                      )}
                    >
                      <td className="px-3 py-3" onClick={(e) => e.stopPropagation()}>
                        {!self && (
                          <input
                            type="checkbox"
                            checked={selectedIds.has(user.id)}
                            onChange={() => toggleSelect(user.id)}
                            className="h-3.5 w-3.5 rounded border-border text-accent focus:ring-accent cursor-pointer"
                          />
                        )}
                      </td>
                      <td className="px-4 py-3 font-medium text-ink">
                        {user.username}
                        {self && (
                          <span className="ml-2 text-xs text-muted-foreground">(you)</span>
                        )}
                      </td>
                      <td className="px-4 py-3" onClick={(e) => e.stopPropagation()}>
                        <Select
                          value={primaryRole}
                          onChange={(e) => handleRoleChange(user, e.target.value)}
                          className="w-32"
                          disabled={updateUser.isPending || self}
                        >
                          {ROLES.map((r) => (
                            <option key={r} value={r}>
                              {r}
                            </option>
                          ))}
                        </Select>
                        <Badge
                          variant={roleBadgeVariant(primaryRole)}
                          className="ml-2 hidden sm:inline-flex"
                        >
                          {primaryRole}
                        </Badge>
                      </td>
                      <td className="px-4 py-3 text-xs text-muted-foreground">
                        {timeAgo(user.createdAt)}
                      </td>
                      <td className="px-4 py-3 text-xs text-muted-foreground">
                        {timeAgo(user.lastLogin)}
                      </td>
                      <td className="px-4 py-3" onClick={(e) => e.stopPropagation()}>
                        <div className="flex justify-end gap-1">
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => setPasswordTarget(user)}
                            title="Reset password"
                          >
                            <KeyRound className="h-3.5 w-3.5" />
                            <span className="ml-1 hidden sm:inline">Reset Password</span>
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-danger hover:bg-danger/10"
                            onClick={() => setDeleteTarget(user)}
                            disabled={self}
                            title={self ? "Cannot delete yourself" : "Delete user"}
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
            </tbody>
          </table>
        </div>
      </div>

      {/* Bulk action bar */}
      {selectedIds.size > 0 && (
        <BulkActionBar
          count={selectedIds.size}
          onChangeRole={(role) => setBulkConfirm({ action: "role", role })}
          onDelete={() => setBulkConfirm({ action: "delete" })}
          onClear={() => setSelectedIds(new Set())}
        />
      )}

      {/* Modals */}
      {showCreate && <CreateUserModal onClose={() => setShowCreate(false)} />}
      {passwordTarget && (
        <ChangePasswordModal
          user={passwordTarget}
          onClose={() => setPasswordTarget(null)}
        />
      )}
      {deleteTarget && (
        <ConfirmDelete
          user={deleteTarget}
          isPending={deleteUser.isPending}
          onConfirm={handleDelete}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
      {bulkConfirm && (
        <BulkConfirmDialog
          count={selectedIds.size}
          action={bulkConfirm.action}
          role={bulkConfirm.role}
          isPending={bulkPending}
          onConfirm={handleBulkConfirm}
          onCancel={() => setBulkConfirm(null)}
        />
      )}
    </div>
  );
}
