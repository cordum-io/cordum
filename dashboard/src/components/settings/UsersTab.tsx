import { useState } from "react";
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
} from "../../hooks/useSettings";
import { post } from "../../api/client";
import { useConfigStore } from "../../state/config";
import type { User } from "../../api/types";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const ROLES = ["Admin", "Operator", "Viewer", "Approver"] as const;

function roleBadgeVariant(
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

// ---------------------------------------------------------------------------
// Create user form
// ---------------------------------------------------------------------------

const createUserSchema = z.object({
  username: z.string().min(3, "Username must be at least 3 characters"),
  password: z.string().min(8, "Password must be at least 8 characters"),
  role: z.string().min(1, "Role is required"),
});

type CreateUserForm = z.infer<typeof createUserSchema>;

// ---------------------------------------------------------------------------
// Change password form
// ---------------------------------------------------------------------------

const changePasswordSchema = z
  .object({
    password: z.string().min(8, "Password must be at least 8 characters"),
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
          <button onClick={onClose} className="rounded-full p-1 hover:bg-surface2">
            <X className="h-4 w-4 text-muted" />
          </button>
        </div>

        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
          <div>
            <label className="mb-1 block text-xs font-semibold text-muted">
              Username
            </label>
            <Input placeholder="e.g. jane.doe" {...register("username")} />
            {errors.username && (
              <p className="mt-1 text-xs text-danger">{errors.username.message}</p>
            )}
          </div>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted">
              Password
            </label>
            <Input type="password" placeholder="Min 8 characters" {...register("password")} />
            {errors.password && (
              <p className="mt-1 text-xs text-danger">{errors.password.message}</p>
            )}
          </div>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted">
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
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<ChangePasswordForm>({
    resolver: zodResolver(changePasswordSchema),
    defaultValues: { password: "", confirm: "" },
  });

  async function onSubmit(data: ChangePasswordForm) {
    setSubmitting(true);
    setError("");
    try {
      await post(`/users/${user.id}/password`, { password: data.password });
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to change password");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="surface-card w-full max-w-md rounded-3xl p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h3 className="font-display text-lg font-semibold text-ink">
            Change Password for {user.username}
          </h3>
          <button onClick={onClose} className="rounded-full p-1 hover:bg-surface2">
            <X className="h-4 w-4 text-muted" />
          </button>
        </div>

        {error && (
          <div className="mb-4 rounded-xl bg-[color:rgba(184,58,58,0.14)] px-4 py-2 text-sm text-danger">
            {error}
          </div>
        )}

        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
          <div>
            <label className="mb-1 block text-xs font-semibold text-muted">
              New Password
            </label>
            <Input type="password" placeholder="Min 8 characters" {...register("password")} />
            {errors.password && (
              <p className="mt-1 text-xs text-danger">{errors.password.message}</p>
            )}
          </div>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted">
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
            <Button type="submit" size="sm" disabled={submitting}>
              {submitting ? "Changing..." : "Change Password"}
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
        <p className="mb-6 text-sm text-muted">
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
// UsersTab
// ---------------------------------------------------------------------------

export function UsersTab() {
  const { data, isLoading, isError } = useUsers();
  const updateUser = useUpdateUser();
  const deleteUser = useDeleteUser();

  const [showCreate, setShowCreate] = useState(false);
  const [passwordTarget, setPasswordTarget] = useState<User | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<User | null>(null);

  const currentUserId = useConfigStore((s) => s.user?.id);
  const currentUsername = useConfigStore((s) => s.user?.username);
  const users = data?.items ?? [];

  function handleRoleChange(user: User, newRole: string) {
    updateUser.mutate({ id: user.id, data: { roles: [newRole] } });
  }

  function handleDelete() {
    if (!deleteTarget) return;
    deleteUser.mutate(deleteTarget.id, {
      onSuccess: () => setDeleteTarget(null),
    });
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
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Username
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Role
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Created
                </th>
                <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-muted">
                  Last Login
                </th>
                <th className="px-4 py-3" />
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {isLoading &&
                Array.from({ length: 3 }, (_, i) => (
                  <tr key={i} className="animate-pulse">
                    {Array.from({ length: 5 }, (_, j) => (
                      <td key={j} className="px-4 py-3">
                        <div className="h-4 rounded bg-surface2 w-3/4" />
                      </td>
                    ))}
                  </tr>
                ))}

              {!isLoading && isError && (
                <tr>
                  <td colSpan={5} className="px-4 py-12 text-center text-muted">
                    Failed to load users.
                  </td>
                </tr>
              )}

              {!isLoading && !isError && users.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-4 py-12 text-center text-muted">
                    No users yet. Create one to get started.
                  </td>
                </tr>
              )}

              {!isLoading &&
                users.map((user) => {
                  const primaryRole = user.roles[0] ?? "Viewer";
                  const isSelf =
                    user.id === currentUserId || user.username === currentUsername;

                  return (
                    <tr
                      key={user.id}
                      className="transition-colors hover:bg-surface2/60"
                    >
                      <td className="px-4 py-3 font-medium text-ink">
                        {user.username}
                        {isSelf && (
                          <span className="ml-2 text-xs text-muted">(you)</span>
                        )}
                      </td>
                      <td className="px-4 py-3">
                        <Select
                          value={primaryRole}
                          onChange={(e) => handleRoleChange(user, e.target.value)}
                          className="w-32"
                          disabled={updateUser.isPending}
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
                      <td className="px-4 py-3 text-xs text-muted">
                        {timeAgo(user.createdAt)}
                      </td>
                      <td className="px-4 py-3 text-xs text-muted">
                        {timeAgo(user.lastLogin)}
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex justify-end gap-1">
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => setPasswordTarget(user)}
                            title="Change password"
                          >
                            <KeyRound className="h-3.5 w-3.5" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-danger hover:bg-danger/10"
                            onClick={() => setDeleteTarget(user)}
                            disabled={isSelf}
                            title={isSelf ? "Cannot delete yourself" : "Delete user"}
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
    </div>
  );
}
