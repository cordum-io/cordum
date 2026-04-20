import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import type { ApprovalStatus } from "@/api/types";

interface ApprovalBadgeProps {
  status?: ApprovalStatus;
  decision?: "approve" | "reject" | "expire" | "invalidate" | "repair";
}

function approvalVariant(status: ApprovalStatus): BadgeVariant {
  switch (status) {
    case "approved":
      return "healthy";
    case "rejected":
      return "danger";
    case "pending":
      return "warning";
    case "invalidated":
      return "governance";
    case "repaired":
      return "info";
    case "expired":
    default:
      return "muted";
  }
}

function approvalLabel(status: ApprovalStatus, decision?: ApprovalBadgeProps["decision"]): string {
  if (status === "pending") return "Approval pending";
  if (status === "approved") return decision === "repair" ? "Repaired" : "Approved";
  if (status === "rejected") return "Rejected";
  if (status === "invalidated") return "Invalidated";
  if (status === "repaired") return "Repaired";
  return "Expired";
}

export function ApprovalBadge({ status, decision }: ApprovalBadgeProps) {
  if (!status) return null;

  return (
    <StatusBadge variant={approvalVariant(status)}>
      {approvalLabel(status, decision)}
    </StatusBadge>
  );
}
