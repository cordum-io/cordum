/*
 * DESIGN: "Control Surface" — Users & RBAC
 * PRD Section 34: User management with roles and invite
 */
import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { get, post, del } from "@/api/client";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonTable, SkeletonCard } from "@/components/ui/Skeleton";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { DialogOverlay } from "@/components/ui/DialogOverlay";
import { Search, UserPlus, Users, Shield, Trash2, Edit, X, Mail, Key } from "lucide-react";
import { cn } from "@/lib/utils";
import { toast } from "sonner";
import { friendlyError } from "@/lib/friendlyError";
import { ErrorBanner } from "@/components/ui/ErrorBanner";

interface User {
  id: string;
  email: string;
  name: string;
  role: "admin" | "operator" | "viewer";
  lastActive: string;
  status: "active" | "invited" | "disabled";
}

const ROLES: { value: string; label: string; desc: string; color: BadgeVariant }[] = [
  { value: "admin", label: "Admin", desc: "Full access to all resources", color: "warning" },
  { value: "operator", label: "Operator", desc: "Manage jobs, workflows, approvals", color: "healthy" },
  { value: "viewer", label: "Viewer", desc: "Read-only access", color: "info" },
];

const ALL_PERMISSIONS = [
  { key: "admin.*", label: "Full Admin", category: "System" },
  { key: "jobs.read", label: "View Jobs", category: "Jobs" },
  { key: "jobs.write", label: "Create/Edit Jobs", category: "Jobs" },
  { key: "jobs.approve", label: "Approve Jobs", category: "Jobs" },
  { key: "agents.read", label: "View Agent Identities", category: "Agents" },
  { key: "agents.write", label: "Manage Agent Identities", category: "Agents" },
  { key: "workflows.read", label: "View Workflows", category: "Workflows" },
  { key: "workflows.write", label: "Create/Edit Workflows", category: "Workflows" },
  { key: "workers.read", label: "View Workers", category: "Workers" },
  { key: "config.read", label: "View Config", category: "Config" },
  { key: "config.write", label: "Edit Config", category: "Config" },
  { key: "audit.read", label: "View Audit Log", category: "Audit" },
  { key: "packs.install", label: "Install Packs", category: "Packs" },
  { key: "packs.uninstall", label: "Uninstall Packs", category: "Packs" },
  { key: "policy.read", label: "View Policies", category: "Policy" },
  { key: "policy.write", label: "Edit Policies", category: "Policy" },
  { key: "schemas.read", label: "View Schemas", category: "Schemas" },
  { key: "schemas.write", label: "Edit Schemas", category: "Schemas" },
  { key: "users.read", label: "View Users", category: "Users" },
  { key: "users.write", label: "Manage Users", category: "Users" },
  { key: "roles.read", label: "View Roles", category: "Roles" },
  { key: "roles.write", label: "Manage Roles", category: "Roles" },
];

const PERMISSION_CATEGORIES = [...new Set(ALL_PERMISSIONS.map(p => p.category))];

function hasPermission(perms: string[], perm: string): boolean {
  if (perms.includes("admin.*")) return true;
  if (perms.includes(perm)) return true;
  const ns = perm.split(".")[0];
  if (perms.includes(`${ns}.*`)) return true;
  return false;
}

export default function SettingsUsersPage() {
  const queryClient = useQueryClient();
  const license = useLicense();
  const rbacEntitled = license.data?.entitlements?.rbac === true;
  const [activeTab, setActiveTab] = useState("users");
  const [search, setSearch] = useState("");
  const [inviteOpen, setInviteOpen] = useState(false);
  const [inviteUsername, setInviteUsername] = useState("");
  const [inviteEmail, setInviteEmail] = useState("");
  const [invitePassword, setInvitePassword] = useState("");
  const [inviteRole, setInviteRole] = useState("operator");
  const [deleteTarget, setDeleteTarget] = useState<User | null>(null);
  const [roleEditOpen, setRoleEditOpen] = useState(false);
  const [editingRole, setEditingRole] = useState<RoleDefinition | null>(null);
  const [roleDeleteTarget, setRoleDeleteTarget] = useState<RoleDefinition | null>(null);
  const [roleName, setRoleName] = useState("");
  const [roleDesc, setRoleDesc] = useState("");
  const [rolePerms, setRolePerms] = useState<string[]>([]);
  const [roleInherits, setRoleInherits] = useState<string[]>([]);

  const { data: users, isLoading, isError, error, refetch } = useQuery({
    queryKey: ["users"],
    queryFn: async () => {
      const res = await get<{ data?: User[] }>("/users");
      return res.data || [];
    },
  });

  const { data: rolesData, isLoading: rolesLoading } = useQuery({
    queryKey: ["auth", "roles"],
    queryFn: () => get<RolesResponse>("/auth/roles"),
  });

  const roles = rolesData?.roles ?? [];

  const resetInviteForm = () => {
    setInviteUsername("");
    setInviteEmail("");
    setInvitePassword("");
    setInviteRole("operator");
  };

  const inviteMutation = useMutation({
    mutationFn: async () => post("/users", { username: inviteUsername, email: inviteEmail, password: invitePassword, role: inviteRole }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      toast.success(`User ${inviteUsername} created`);
      setInviteOpen(false);
      resetInviteForm();
    },
    onError: (err: Error) => {
      { const f = friendlyError(err, "create user"); toast.error(f.title, { description: f.description }); };
    },
  });

  const updateRoleMutation = useMutation({
    mutationFn: async ({ id, role }: { id: string; role: string }) =>
      put(`/users/${id}`, { roles: [role] }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      toast.success("Role updated");
    },
    onError: (err: Error) => {
      { const f = friendlyError(err, "update role"); toast.error(f.title, { description: f.description }); };
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => del(`/users/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      toast.success("User removed");
      setDeleteTarget(null);
    },
    onError: (err: Error) => {
      { const f = friendlyError(err, "remove user"); toast.error(f.title, { description: f.description }); };
    },
  });

  const saveRoleMutation = useMutation({
    mutationFn: async () => {
      const name = editingRole ? editingRole.name : roleName.toLowerCase().trim().replace(/\s+/g, "_");
      return put(`/auth/roles/${name}`, {
        description: roleDesc,
        permissions: rolePerms,
        inherits: roleInherits,
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["auth", "roles"] });
      toast.success(editingRole ? "Role updated" : "Role created");
      closeRoleEditor();
    },
    onError: (err: Error) => {
      { const f = friendlyError(err, editingRole ? "update role" : "create role"); toast.error(f.title, { description: f.description }); };
    },
  });

  const deleteRoleMutation = useMutation({
    mutationFn: async (name: string) => del(`/auth/roles/${name}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["auth", "roles"] });
      toast.success("Role deleted");
      setRoleDeleteTarget(null);
    },
    onError: (err: Error) => {
      { const f = friendlyError(err, "delete role"); toast.error(f.title, { description: f.description }); };
    },
  });

  function openRoleEditor(role?: RoleDefinition) {
    if (role) {
      setEditingRole(role);
      setRoleName(role.name);
      setRoleDesc(role.description);
      setRolePerms([...role.permissions]);
      setRoleInherits([...role.inherits]);
    } else {
      setEditingRole(null);
      setRoleName("");
      setRoleDesc("");
      setRolePerms([]);
      setRoleInherits([]);
    }
    setRoleEditOpen(true);
  }

  function closeRoleEditor() {
    setRoleEditOpen(false);
    setEditingRole(null);
    setRoleName("");
    setRoleDesc("");
    setRolePerms([]);
    setRoleInherits([]);
  }

  function togglePerm(perm: string) {
    setRolePerms(prev =>
      prev.includes(perm) ? prev.filter(p => p !== perm) : [...prev, perm]
    );
  }

  const filtered = (users || []).filter(u =>
    !search || u.email.toLowerCase().includes(search.toLowerCase()) || u.name.toLowerCase().includes(search.toLowerCase())
  );
  const totalUsers = users?.length ?? 0;
  const activeUsers = users?.filter((user) => user.status === "active").length ?? 0;
  const invitedUsers = users?.filter((user) => user.status === "invited").length ?? 0;
  const customRoles = roles.filter((role) => !role.built_in).length;
  const summaryLoading = isLoading || rolesLoading;

  if (isError) {
    return <ErrorBanner message={error instanceof Error ? error.message : "Failed to load users"} onRetry={() => void refetch()} />;
  }

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader
        title="Users & RBAC"
        subtitle="Manage team access and role-based permissions"
        actions={
          activeTab === "roles" && rbacEntitled ? (
            <Button variant="primary" size="sm" onClick={() => openRoleEditor()}>
              <Plus className="w-3 h-3 mr-1" />
              Custom Role
            </Button>
          ) : (
            <Button variant="primary" size="sm" onClick={() => setInviteOpen(true)}>
              <UserPlus className="w-3 h-3 mr-1" />
              Create User
            </Button>
          )
        }
      />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
        {summaryLoading ? (
          Array.from({ length: 4 }).map((_, index) => <SkeletonCard key={index} />)
        ) : (
          <>
            <StatTile
              accent="cordum"
              label="Users"
              value={totalUsers}
              helperText="Total directory entries"
              icon={<Users className="h-4 w-4" />}
            />
            <StatTile
              accent={activeUsers > 0 ? "healthy" : "muted"}
              label="Active"
              value={activeUsers}
              helperText="Signed-in team members"
              icon={<Check className="h-4 w-4" />}
            />
            <StatTile
              accent={invitedUsers > 0 ? "warning" : "muted"}
              label="Invited"
              value={invitedUsers}
              helperText="Awaiting first login"
              icon={<Mail className="h-4 w-4" />}
            />
            <StatTile
              accent={customRoles > 0 ? "info" : "muted"}
              label="Custom roles"
              value={customRoles}
              helperText={`${roles.length} total roles`}
              icon={<Shield className="h-4 w-4" />}
            />
          </>
        )}
      </div>

      <InstrumentCard className="p-4">
        <InstrumentCardBody className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
          <Tabs
            ariaLabel="User and role views"
            variant="segmented"
            className="w-full lg:w-auto"
            activeTab={activeTab}
            onChange={setActiveTab}
            tabs={[
              { id: "users", label: "Users", count: totalUsers },
              { id: "roles", label: "Roles", count: roles.length },
            ]}
          />
          {activeTab === "users" ? (
            <div className="w-full max-w-sm">
              <Input
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search users..."
                aria-label="Search users"
                icon={<Search className="h-3.5 w-3.5" />}
                className="bg-surface-1"
              />
            </div>
          ) : (
            <p className="max-w-md text-sm text-muted-foreground">
              Review the built-in roles, then extend them with custom enterprise RBAC roles when you need tighter permission boundaries.
            </p>
          )}
        </InstrumentCardBody>
      </InstrumentCard>

      {/* Users Tab */}
      {activeTab === "users" && (
        isLoading ? <SkeletonTable rows={5} /> :
        filtered.length === 0 ? <EmptyState icon={<Users className="w-8 h-8" />} title="No users found" description="Invite team members to get started" /> : (
          <div className="instrument-card overflow-hidden">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border bg-surface-0">
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">User</th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Role</th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Status</th>
                  <th className="text-left px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Last Active</th>
                  <th className="text-right px-5 py-3 text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest">Actions</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((user, i) => (
                  <motion.tr
                    key={user.id}
                    initial={{ opacity: 0 }}
                    animate={{ opacity: 1 }}
                    transition={{ delay: i * 0.03 }}
                    className="border-b border-border last:border-0 hover:bg-surface-1 transition-colors"
                  >
                    <td className="px-5 py-3">
                      <div>
                        <p className="text-sm font-medium text-foreground">{user.name}</p>
                        <p className="text-xs text-muted-foreground">{user.email}</p>
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <StatusBadge variant={ROLES.find(r => r.value === user.role)?.color || "muted"}>{user.role}</StatusBadge>
                    </td>
                    <td className="px-5 py-3">
                      <StatusBadge variant={user.status === "active" ? "healthy" : user.status === "invited" ? "warning" : "danger"}>{user.status}</StatusBadge>
                    </td>
                    <td className="px-5 py-3 text-xs text-muted-foreground">{user.lastActive}</td>
                    <td className="px-5 py-3 text-right">
                      <button type="button" onClick={() => setDeleteTarget(user)} className="p-1.5 rounded hover:bg-destructive/10 transition-colors">
                        <Trash2 className="w-3.5 h-3.5 text-destructive" />
                      </button>
                    </td>
                  </motion.tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      )}

      {/* Roles Tab */}
      {activeTab === "roles" && (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          {ROLES.map((role, i) => (
            <motion.div
              key={role.value}
              initial={{ opacity: 0, y: 8 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ delay: i * 0.05 }}
              className="instrument-card p-5"
            >
              <div className="flex items-center gap-2 mb-3">
                <Shield className="w-4 h-4 text-cordum" />
                <span className="text-sm font-display font-semibold text-foreground capitalize">{role.label}</span>
                <StatusBadge variant={role.color}>{role.value}</StatusBadge>
              </div>

              {/* Permissions matrix */}
              {roles.length > 0 && (
                <motion.div
                  initial={{ opacity: 0, y: 8 }}
                  animate={{ opacity: 1, y: 0 }}
                  transition={{ delay: 0.2 }}
                  className="instrument-card overflow-x-auto"
                >
                  <h3 className="text-sm font-display font-semibold text-foreground mb-4">Permission Matrix</h3>
                  <table className="w-full text-xs">
                    <thead>
                      <tr className="border-b border-border">
                        <th className="text-left px-3 py-2 font-mono font-medium text-muted-foreground uppercase tracking-widest min-w-[180px]">Permission</th>
                        {roles.map(r => (
                          <th key={r.name} className="text-center px-3 py-2 font-mono font-medium text-muted-foreground uppercase tracking-widest min-w-[80px] capitalize">{r.name}</th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {PERMISSION_CATEGORIES.map(cat => (
                        <>
                          <tr key={`cat-${cat}`}>
                            <td colSpan={roles.length + 1} className="px-3 pt-3 pb-1 text-[10px] font-mono font-semibold text-cordum uppercase tracking-widest">{cat}</td>
                          </tr>
                          {ALL_PERMISSIONS.filter(p => p.category === cat && p.key !== "admin.*").map(perm => (
                            <tr key={perm.key} className="border-b border-border/50 last:border-0 hover:bg-surface-1/50 transition-colors">
                              <td className="px-3 py-1.5 text-foreground">{perm.label}</td>
                              {roles.map(r => (
                                <td key={r.name} className="text-center px-3 py-1.5">
                                  {hasPermission(r.permissions, perm.key) ? (
                                    <Check className="mx-auto h-3.5 w-3.5 text-success" />
                                  ) : (
                                    <span className="block w-3.5 h-3.5 mx-auto text-border">—</span>
                                  )}
                                </td>
                              ))}
                            </tr>
                          ))}
                        </>
                      ))}
                    </tbody>
                  </table>
                </motion.div>
              )}
            </>
          )}
        </div>
      )}

      {/* Invite Dialog */}
      <DialogOverlay open={inviteOpen} onClose={() => setInviteOpen(false)} label="Invite user" className="w-[420px] bg-surface-1 border border-border rounded-xl shadow-2xl p-6">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-sm font-display font-semibold text-foreground">Invite User</h3>
          <button onClick={() => setInviteOpen(false)} className="p-1 rounded hover:bg-surface-2 transition-colors">
            <X className="w-4 h-4 text-muted-foreground" />
          </button>
        </div>
        <div className="space-y-4">
          <div>
            <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Email</label>
            <div className="relative">
              <Mail className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
              <input
                type="email"
                value={inviteEmail}
                onChange={(e) => setInviteEmail(e.target.value)}
                placeholder="user@company.com"
                className="h-9 w-full pl-9 pr-3 text-sm bg-surface-2 border border-border rounded-md text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
              />
            </div>
          </div>
          <div>
            <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Role</label>
            <select
              value={inviteRole}
              onChange={(e) => setInviteRole(e.target.value)}
              className="h-9 w-full px-3 text-sm bg-surface-2 border border-border rounded-md text-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
            >
              {ROLES.map(r => <option key={r.value} value={r.value}>{r.label} — {r.desc}</option>)}
            </select>
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="ghost" size="sm" onClick={() => setInviteOpen(false)}>Cancel</Button>
            <Button variant="primary" size="sm" onClick={() => inviteMutation.mutate()} loading={inviteMutation.isPending} disabled={!inviteEmail.trim()}>
              <UserPlus className="w-3 h-3 mr-1" />Send Invite
            </Button>
          </div>
        </div>
      </DialogOverlay>

          <div className="flex justify-end gap-2 pt-2 border-t border-border">
            <Button variant="ghost" size="sm" onClick={closeRoleEditor}>Cancel</Button>
            <Button
              variant="primary"
              size="sm"
              onClick={() => saveRoleMutation.mutate()}
              loading={saveRoleMutation.isPending}
              disabled={!editingRole && !roleName.trim()}
            >
              <Shield className="w-3 h-3 mr-1" />{editingRole ? "Update Role" : "Create Role"}
            </Button>
          </div>
        </div>
      </DialogOverlay>

      {/* Delete User Confirmation */}
      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        title="Remove User"
        description={`Are you sure you want to remove ${deleteTarget?.name}? They will lose all access to this cluster.`}
        confirmLabel="Remove"
        variant="destructive"
      />

      {/* Delete Role Confirmation */}
      <ConfirmDialog
        open={!!roleDeleteTarget}
        onClose={() => setRoleDeleteTarget(null)}
        onConfirm={() => roleDeleteTarget && deleteRoleMutation.mutate(roleDeleteTarget.name)}
        title="Delete Role"
        description={`Are you sure you want to delete the "${roleDeleteTarget?.name}" role? Users with this role will need to be reassigned.`}
        confirmLabel="Delete"
        variant="destructive"
      />
    </motion.div>
  );
}
