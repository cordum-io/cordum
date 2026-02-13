import { Badge } from "../ui/Badge";
import { Card } from "../ui/Card";
import { CheckCircle, XCircle } from "lucide-react";

// ---------------------------------------------------------------------------
// Permission definitions
// ---------------------------------------------------------------------------

const RESOURCES = [
  "Jobs",
  "Workflows",
  "Policies",
  "Approvals",
  "Settings",
  "Audit",
  "DLQ",
  "Packs",
] as const;

const PERMISSIONS = ["read", "write", "admin"] as const;

type Permission = (typeof PERMISSIONS)[number];

const ROLE_PERMISSIONS: Record<string, Record<string, Permission[]>> = {
  Admin: Object.fromEntries(RESOURCES.map((r) => [r, ["read", "write", "admin"]])),
  Operator: {
    Jobs: ["read", "write"],
    Workflows: ["read", "write"],
    Policies: ["read"],
    Approvals: ["read", "write"],
    Settings: ["read"],
    Audit: ["read"],
    DLQ: ["read", "write"],
    Packs: ["read", "write"],
  },
  Approver: {
    Jobs: ["read"],
    Workflows: ["read"],
    Policies: ["read"],
    Approvals: ["read", "write"],
    Settings: [],
    Audit: ["read"],
    DLQ: ["read"],
    Packs: ["read"],
  },
  Viewer: {
    Jobs: ["read"],
    Workflows: ["read"],
    Policies: ["read"],
    Approvals: ["read"],
    Settings: [],
    Audit: ["read"],
    DLQ: ["read"],
    Packs: ["read"],
  },
};

const ROLE_ORDER = ["Admin", "Operator", "Approver", "Viewer"] as const;

function roleBadgeVariant(role: string): "success" | "warning" | "info" | "default" {
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

// ---------------------------------------------------------------------------
// PermissionMatrix
// ---------------------------------------------------------------------------

export function PermissionMatrix() {
  return (
    <Card>
      <p className="mb-3 text-xs font-semibold uppercase tracking-wide text-muted">
        Permission Matrix
      </p>
      <div className="overflow-x-auto">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-border">
              <th className="px-3 py-2 text-left font-semibold text-muted">Resource</th>
              {ROLE_ORDER.map((role) => (
                <th key={role} className="px-3 py-2 text-center" colSpan={PERMISSIONS.length}>
                  <Badge variant={roleBadgeVariant(role)}>{role}</Badge>
                </th>
              ))}
            </tr>
            <tr className="border-b border-border">
              <th />
              {ROLE_ORDER.map((role) =>
                PERMISSIONS.map((perm) => (
                  <th
                    key={`${role}-${perm}`}
                    className="px-1.5 py-1 text-center text-[10px] font-medium uppercase text-muted"
                  >
                    {perm[0]?.toUpperCase()}
                  </th>
                )),
              )}
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {RESOURCES.map((resource) => (
              <tr key={resource} className="hover:bg-surface2/40 transition-colors">
                <td className="px-3 py-2 font-medium text-ink">{resource}</td>
                {ROLE_ORDER.map((role) =>
                  PERMISSIONS.map((perm) => {
                    const has = ROLE_PERMISSIONS[role]?.[resource]?.includes(perm) ?? false;
                    const isAdmin = role === "Admin";
                    return (
                      <td key={`${role}-${resource}-${perm}`} className="px-1.5 py-2 text-center">
                        {has ? (
                          <CheckCircle className="mx-auto h-3.5 w-3.5 text-success" />
                        ) : (
                          <XCircle className="mx-auto h-3.5 w-3.5 text-muted/30" />
                        )}
                      </td>
                    );
                  }),
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="mt-2 text-[10px] text-muted">
        R = Read, W = Write, A = Admin. Role permissions are read-only.
      </p>
    </Card>
  );
}
