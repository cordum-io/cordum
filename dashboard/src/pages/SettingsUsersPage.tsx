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
import { SkeletonTable } from "@/components/ui/Skeleton";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { DialogOverlay } from "@/components/ui/DialogOverlay";
import { Search, UserPlus, Users, Shield, Trash2, X, Mail, Key } from "lucide-react";
import { cn } from "@/lib/utils";
import { toast } from "sonner";

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

export default function SettingsUsersPage() {
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState("users");
  const [search, setSearch] = useState("");
  const [inviteOpen, setInviteOpen] = useState(false);
  const [inviteUsername, setInviteUsername] = useState("");
  const [inviteEmail, setInviteEmail] = useState("");
  const [invitePassword, setInvitePassword] = useState("");
  const [inviteRole, setInviteRole] = useState("operator");
  const [deleteTarget, setDeleteTarget] = useState<User | null>(null);

  const { data: users, isLoading } = useQuery({
    queryKey: ["users"],
    queryFn: async () => {
      const res = await get<{ data?: User[] }>("/users");
      return res.data || [];
    },
  });

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
      toast.error("Failed to create user", { description: err.message });
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
      toast.error("Failed to remove user", { description: err.message });
    },
  });

  const tabs = ["users", "roles"];
  const filtered = (users || []).filter(u =>
    !search || u.email.toLowerCase().includes(search.toLowerCase()) || u.name.toLowerCase().includes(search.toLowerCase())
  );

  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} className="space-y-6">
      <PageHeader title="Users & RBAC" subtitle="Manage team access and role-based permissions" actions={<><Button variant="primary" size="sm" onClick={() => setInviteOpen(true)}>
          <UserPlus className="w-3 h-3 mr-1" />Create User
        </Button></>} />

      {/* Tabs */}
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-1 p-1 rounded-2xl bg-surface-1">
          {tabs.map(tab => (
            <button
              key={tab}
              onClick={() => setActiveTab(tab)}
              className={cn(
                "px-4 py-1.5 text-xs font-medium rounded-2xl transition-colors capitalize",
                activeTab === tab ? "bg-cordum/10 text-cordum" : "text-muted-foreground hover:text-foreground",
              )}
            >
              {tab}
            </button>
          ))}
        </div>
        {activeTab === "users" && (
          <div className="relative w-64">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search users..."
              className="h-8 w-full pl-9 pr-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
            />
          </div>
        )}
      </div>

      {/* Users Tab */}
      {activeTab === "users" && (
        isLoading ? <SkeletonTable rows={5} /> :
        filtered.length === 0 ? <EmptyState icon={<Users className="w-8 h-8" />} title="No users found" description="Invite team members to get started" /> : (
          <div className="instrument-card overflow-hidden">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border bg-surface-0">
                  <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">User</th>
                  <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Role</th>
                  <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Status</th>
                  <th className="text-left px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Last Active</th>
                  <th className="text-right px-4 py-3 text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Actions</th>
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
                    <td className="px-4 py-3">
                      <div>
                        <p className="text-sm font-medium text-foreground">{user.name}</p>
                        <p className="text-xs text-muted-foreground">{user.email}</p>
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <StatusBadge variant={ROLES.find(r => r.value === user.role)?.color || "muted"}>{user.role}</StatusBadge>
                    </td>
                    <td className="px-4 py-3">
                      <StatusBadge variant={user.status === "active" ? "healthy" : user.status === "invited" ? "warning" : "danger"}>{user.status}</StatusBadge>
                    </td>
                    <td className="px-4 py-3 text-xs text-muted-foreground">{user.lastActive}</td>
                    <td className="px-4 py-3 text-right">
                      <button onClick={() => setDeleteTarget(user)} className="p-1.5 rounded hover:bg-destructive/10 transition-colors">
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
              className="instrument-card"
            >
              <div className="flex items-center gap-2 mb-3">
                <Shield className="w-4 h-4 text-cordum" />
                <span className="text-sm font-display font-semibold text-foreground capitalize">{role.label}</span>
                <StatusBadge variant={role.color}>{role.value}</StatusBadge>
              </div>
              <p className="text-xs text-muted-foreground">{role.desc}</p>
              <div className="mt-3 pt-3 border-t border-border">
                <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider mb-2">Permissions</p>
                <div className="space-y-1">
                  {role.value === "admin" && ["All resources", "User management", "System config", "API keys"].map(p => (
                    <p key={p} className="text-xs text-foreground">&#x2713; {p}</p>
                  ))}
                  {role.value === "operator" && ["Jobs & Workflows", "Approvals", "Packs", "Schemas"].map(p => (
                    <p key={p} className="text-xs text-foreground">&#x2713; {p}</p>
                  ))}
                  {role.value === "viewer" && ["Read all resources", "View audit log", "View dashboards"].map(p => (
                    <p key={p} className="text-xs text-foreground">&#x2713; {p}</p>
                  ))}
                </div>
              </div>
            </motion.div>
          ))}
        </div>
      )}

      {/* Create User Dialog */}
      <DialogOverlay open={inviteOpen} onClose={() => { setInviteOpen(false); resetInviteForm(); }} label="Create user" className="w-[420px] bg-surface-1 border border-border rounded-xl shadow-2xl p-6">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-sm font-display font-semibold text-foreground">Create User</h3>
          <button onClick={() => { setInviteOpen(false); resetInviteForm(); }} className="p-1 rounded hover:bg-surface-2 transition-colors">
            <X className="w-4 h-4 text-muted-foreground" />
          </button>
        </div>
        <div className="space-y-4">
          <div>
            <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Username</label>
            <input
              type="text"
              value={inviteUsername}
              onChange={(e) => setInviteUsername(e.target.value)}
              placeholder="jsmith"
              className="h-9 w-full px-3 text-sm bg-surface-2 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
            />
          </div>
          <div>
            <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Email</label>
            <div className="relative">
              <Mail className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
              <input
                type="email"
                value={inviteEmail}
                onChange={(e) => setInviteEmail(e.target.value)}
                placeholder="user@company.com"
                className="h-9 w-full pl-9 pr-3 text-sm bg-surface-2 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
              />
            </div>
          </div>
          <div>
            <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Password</label>
            <div className="relative">
              <Key className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
              <input
                type="password"
                value={invitePassword}
                onChange={(e) => setInvitePassword(e.target.value)}
                placeholder="Minimum 8 characters"
                className="h-9 w-full pl-9 pr-3 text-sm bg-surface-2 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
              />
            </div>
          </div>
          <div>
            <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Role</label>
            <select
              value={inviteRole}
              onChange={(e) => setInviteRole(e.target.value)}
              className="h-9 w-full px-3 text-sm bg-surface-2 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
            >
              {ROLES.map(r => <option key={r.value} value={r.value}>{r.label} — {r.desc}</option>)}
            </select>
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="ghost" size="sm" onClick={() => { setInviteOpen(false); resetInviteForm(); }}>Cancel</Button>
            <Button variant="primary" size="sm" onClick={() => inviteMutation.mutate()} loading={inviteMutation.isPending} disabled={!inviteUsername.trim() || !invitePassword.trim()}>
              <UserPlus className="w-3 h-3 mr-1" />Create User
            </Button>
          </div>
        </div>
      </DialogOverlay>

      {/* Delete Confirmation */}
      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        title="Remove User"
        description={`Are you sure you want to remove ${deleteTarget?.name}? They will lose all access to this cluster.`}
        confirmLabel="Remove"
        variant="destructive"
      />
    </motion.div>
  );
}
