/*
 * DESIGN: "Control Surface" — Users & RBAC
 * PRD Section 34: User management with roles and invite
 */
import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { get, post, put, del } from "@/api/client";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/Button";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonTable, SkeletonCard } from "@/components/ui/Skeleton";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { DialogOverlay } from "@/components/ui/DialogOverlay";
import { Input } from "@/components/ui/Input";
import { Select } from "@/components/ui/Select";
import { Tabs } from "@/components/ui/Tabs";
import { LabeledField } from "@/components/ui/LabeledField";
import { StatTile } from "@/components/ui/StatTile";
import { InstrumentCard, InstrumentCardBody } from "@/components/ui/InstrumentCard";
import { Checkbox } from "@/components/ui/Checkbox";
import { UpgradePrompt } from "@/components/UpgradePrompt";
import { useLicense } from "@/hooks/useLicense";
import { Search, UserPlus, Users, Shield, Trash2, X, Mail, Key, Plus, Check } from "lucide-react";
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

interface RoleDefinition {
  name: string;
  description: string;
  permissions: string[];
  inherits: string[];
  built_in: boolean;
  created_at?: string;
  updated_at?: string;
}

interface RolesResponse {
  roles: RoleDefinition[];
  entitled: boolean;
}

const BASIC_ROLES: { value: string; label: string; desc: string; color: BadgeVariant }[] = [
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
                    <td className="px-5 py-3">
                      <Select
                        value={user.role}
                        onChange={(e) => updateRoleMutation.mutate({ id: user.id, role: e.target.value })}
                        disabled={updateRoleMutation.isPending}
                        className="h-7 max-w-[180px] rounded-lg bg-surface-0 px-2 py-0 text-xs font-medium"
                      >
                        {BASIC_ROLES.map(r => <option key={r.value} value={r.value}>{r.label}</option>)}
                        {roles.filter(r => !r.built_in).map(r => (
                          <option key={r.name} value={r.name}>{r.name}</option>
                        ))}
                      </Select>
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
        <div className="space-y-4">
          {!rbacEntitled && (
            <UpgradePrompt
              label="Advanced RBAC"
              plan={license.data?.plan}
              forceVisible
              title="Custom roles require Enterprise"
              description="Create custom roles with granular permission sets. Built-in roles (Admin, Operator, Viewer) are available on all plans."
            />
          )}

          {rolesLoading ? (
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              <SkeletonCard /><SkeletonCard /><SkeletonCard />
            </div>
          ) : (
            <>
              {/* Role cards */}
              <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                {roles.map((role, i) => (
                  <motion.div
                    key={role.name}
                    initial={{ opacity: 0, y: 8 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ delay: i * 0.05 }}
                    className="instrument-card group"
                  >
                    <div className="flex items-center justify-between mb-3">
                      <div className="flex items-center gap-2">
                        <Shield className="w-4 h-4 text-cordum" />
                        <span className="text-sm font-display font-semibold text-foreground capitalize">{role.name}</span>
                        {role.built_in ? (
                          <StatusBadge variant="info">built-in</StatusBadge>
                        ) : (
                          <StatusBadge variant="healthy">custom</StatusBadge>
                        )}
                      </div>
                      {rbacEntitled && (
                        <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                          <button
                            type="button"
                            onClick={() => openRoleEditor(role)}
                            className="p-1 rounded hover:bg-surface-2 transition-colors"
                            title="Edit role"
                          >
                            <Shield className="w-3 h-3 text-muted-foreground" />
                          </button>
                          {!role.built_in && (
                            <button
                              type="button"
                              onClick={() => setRoleDeleteTarget(role)}
                              className="p-1 rounded hover:bg-destructive/10 transition-colors"
                              title="Delete role"
                            >
                              <Trash2 className="w-3 h-3 text-destructive" />
                            </button>
                          )}
                        </div>
                      )}
                    </div>
                    <p className="text-xs text-muted-foreground mb-3">{role.description || "No description"}</p>
                    {role.inherits.length > 0 && (
                      <p className="text-xs text-muted-foreground mb-2">
                        Inherits: {role.inherits.map(r => <span key={r} className="inline-flex items-center gap-0.5 mr-1 px-1.5 py-0.5 rounded bg-surface-2 text-foreground text-[10px] font-medium">{r}</span>)}
                      </p>
                    )}
                    <div className="pt-3 border-t border-border">
                      <p className="text-xs font-mono font-medium text-muted-foreground uppercase tracking-widest mb-2">Permissions</p>
                      <div className="flex flex-wrap gap-1">
                        {role.permissions.includes("admin.*") ? (
                          <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-cordum/10 text-cordum text-[10px] font-medium">
                            <Check className="w-2.5 h-2.5" />All access
                          </span>
                        ) : (
                          role.permissions.slice(0, 6).map(p => (
                            <span key={p} className="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded bg-surface-2 text-foreground text-[10px] font-mono">
                              {p}
                            </span>
                          ))
                        )}
                        {!role.permissions.includes("admin.*") && role.permissions.length > 6 && (
                          <span className="inline-flex items-center px-1.5 py-0.5 rounded bg-surface-2 text-muted-foreground text-[10px]">
                            +{role.permissions.length - 6} more
                          </span>
                        )}
                      </div>
                    </div>
                  </motion.div>
                ))}
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

      {/* Create User Dialog */}
      <DialogOverlay open={inviteOpen} onClose={() => { setInviteOpen(false); resetInviteForm(); }} label="Create user" className="w-[420px] bg-surface-1 border border-border rounded-xl shadow-2xl p-6">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-sm font-display font-semibold text-foreground">Create User</h2>
          <button type="button" onClick={() => { setInviteOpen(false); resetInviteForm(); }} className="p-1 rounded hover:bg-surface-2 transition-colors">
            <X className="w-4 h-4 text-muted-foreground" />
          </button>
        </div>
        <div className="space-y-4">
          <LabeledField label="Username">
            <Input
              type="text"
              value={inviteUsername}
              onChange={(e) => setInviteUsername(e.target.value)}
              placeholder="jsmith"
              className="bg-surface-2"
            />
          </LabeledField>
          <LabeledField label="Email">
            <Input
              type="email"
              value={inviteEmail}
              onChange={(e) => setInviteEmail(e.target.value)}
              placeholder="user@company.com"
              icon={<Mail className="h-3.5 w-3.5" />}
              className="bg-surface-2"
            />
          </LabeledField>
          <LabeledField label="Password">
            <Input
              type="password"
              value={invitePassword}
              onChange={(e) => setInvitePassword(e.target.value)}
              placeholder="Minimum 8 characters"
              icon={<Key className="h-3.5 w-3.5" />}
              className="bg-surface-2"
            />
          </LabeledField>
          <LabeledField label="Role">
            <Select
              value={inviteRole}
              onChange={(e) => setInviteRole(e.target.value)}
              className="bg-surface-2"
            >
              {BASIC_ROLES.map(r => <option key={r.value} value={r.value}>{r.label} — {r.desc}</option>)}
            </Select>
          </LabeledField>
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="ghost" size="sm" onClick={() => { setInviteOpen(false); resetInviteForm(); }}>Cancel</Button>
            <Button variant="primary" size="sm" onClick={() => inviteMutation.mutate()} loading={inviteMutation.isPending} disabled={!inviteUsername.trim() || !invitePassword.trim()}>
              <UserPlus className="w-3 h-3 mr-1" />Create User
            </Button>
          </div>
        </div>
      </DialogOverlay>

      {/* Role Editor Dialog */}
      <DialogOverlay open={roleEditOpen} onClose={closeRoleEditor} label={editingRole ? "Edit role" : "Create role"} className="w-[520px] max-h-[80vh] overflow-y-auto bg-surface-1 border border-border rounded-xl shadow-2xl p-6">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-sm font-display font-semibold text-foreground">{editingRole ? `Edit Role: ${editingRole.name}` : "Create Custom Role"}</h2>
          <button type="button" onClick={closeRoleEditor} className="p-1 rounded hover:bg-surface-2 transition-colors">
            <X className="w-4 h-4 text-muted-foreground" />
          </button>
        </div>
        <div className="space-y-4">
          {!editingRole && (
            <LabeledField label="Role name">
              <Input
                type="text"
                value={roleName}
                onChange={(e) => setRoleName(e.target.value)}
                placeholder="devops_engineer"
                className="bg-surface-2"
              />
            </LabeledField>
          )}
          <LabeledField label="Description">
            <Input
              type="text"
              value={roleDesc}
              onChange={(e) => setRoleDesc(e.target.value)}
              placeholder="What this role is for"
              className="bg-surface-2"
            />
          </LabeledField>

          {/* Inherit from */}
          {!(editingRole?.built_in) && (
            <LabeledField
              label="Inherits from"
              description="Start from a related role, then layer in extra permissions."
            >
              <div className="flex flex-wrap gap-2">
                {roles.filter(r => r.name !== editingRole?.name).map(r => (
                  <button
                    key={r.name}
                    type="button"
                    onClick={() => setRoleInherits(prev => prev.includes(r.name) ? prev.filter(n => n !== r.name) : [...prev, r.name])}
                    className={cn(
                      "px-2.5 py-1 text-xs rounded-lg border transition-colors capitalize",
                      roleInherits.includes(r.name)
                        ? "border-cordum bg-cordum/10 text-cordum"
                        : "border-border bg-surface-2 text-muted-foreground hover:text-foreground",
                    )}
                  >
                    {r.name}
                  </button>
                ))}
              </div>
            </LabeledField>
          )}

          {/* Permissions */}
          <LabeledField
            label="Permissions"
            description="Select the capabilities this role grants across each product area."
          >
            <div className="space-y-3 max-h-[300px] overflow-y-auto pr-1">
              {PERMISSION_CATEGORIES.map(cat => (
                <div key={cat}>
                  <p className="text-[10px] font-mono font-semibold text-cordum uppercase tracking-widest mb-1">{cat}</p>
                  <div className="space-y-0.5">
                    {ALL_PERMISSIONS.filter(p => p.category === cat).map(perm => {
                      const checked = rolePerms.includes(perm.key);
                      const disabled = editingRole?.built_in && perm.key === "admin.*";
                      return (
                        <div
                          key={perm.key}
                          className={cn(
                            "flex items-center gap-2 rounded px-2 py-1 transition-colors hover:bg-surface-2/50",
                            disabled && "opacity-50",
                          )}
                        >
                          <Checkbox
                            checked={checked}
                            onChange={() => !disabled && togglePerm(perm.key)}
                            disabled={disabled}
                            aria-label={`${perm.label} permission`}
                          />
                          <span className="text-xs text-foreground">{perm.label}</span>
                          <span className="text-[10px] font-mono text-muted-foreground ml-auto">{perm.key}</span>
                        </div>
                      );
                    })}
                  </div>
                </div>
              ))}
            </div>
          </LabeledField>

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
