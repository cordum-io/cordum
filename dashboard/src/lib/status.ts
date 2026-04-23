import type { ComponentType, SVGProps } from "react";
import {
  CheckCircle,
  Clock,
  Loader,
  XCircle,
  AlertTriangle,
  Circle,
  Shield,
  ShieldOff,
} from "lucide-react";
import { formatStatusToken } from "./format";

type IconComponent = ComponentType<SVGProps<SVGSVGElement> & { className?: string }>;

export interface StatusMeta {
  label: string;
  tone: "success" | "warning" | "danger" | "info" | "muted";
  shape: "circle" | "diamond" | "square" | "shield" | "triangle";
  icon: IconComponent;
}

export function runStatusMeta(status?: string): StatusMeta {
  switch (status) {
    case "succeeded":
      return { label: "succeeded", tone: "success", shape: "circle", icon: CheckCircle };
    case "waiting":
      return { label: "waiting", tone: "warning", shape: "circle", icon: Clock };
    case "running":
      return { label: "running", tone: "info", shape: "circle", icon: Loader };
    case "failed":
      return { label: "failed", tone: "danger", shape: "circle", icon: XCircle };
    case "timed_out":
      return { label: "timed out", tone: "danger", shape: "circle", icon: XCircle };
    case "pending":
      return { label: "pending", tone: "warning", shape: "circle", icon: Clock };
    case "cancelled":
      return { label: "cancelled", tone: "muted", shape: "circle", icon: XCircle };
    default:
      return { label: formatStatusToken(status, "unknown"), tone: "muted", shape: "circle", icon: Circle };
  }
}

export function jobStatusMeta(state?: string): StatusMeta {
  switch (state) {
    case "succeeded":
      return { label: "succeeded", tone: "success", shape: "diamond", icon: CheckCircle };
    case "running":
    case "dispatched":
      return { label: formatStatusToken(state), tone: "info", shape: "diamond", icon: Loader };
    case "scheduled":
      return { label: "scheduled", tone: "info", shape: "diamond", icon: Clock };
    case "approval_required":
      return { label: "approval required", tone: "warning", shape: "shield", icon: Shield };
    case "output_quarantined":
      return { label: "output quarantined", tone: "warning", shape: "shield", icon: AlertTriangle };
    case "failed":
    case "denied":
    case "timeout":
      return { label: formatStatusToken(state), tone: "danger", shape: "diamond", icon: XCircle };
    case "pending":
      return { label: "pending", tone: "warning", shape: "diamond", icon: Clock };
    case "cancelled":
      return { label: "cancelled", tone: "muted", shape: "diamond", icon: XCircle };
    default:
      return { label: formatStatusToken(state, "unknown"), tone: "muted", shape: "diamond", icon: Circle };
  }
}

export function approvalStatusMeta(required?: boolean): StatusMeta {
  if (required) {
    return { label: "approval required", tone: "warning", shape: "shield", icon: Shield };
  }
  return { label: "no approval", tone: "muted", shape: "shield", icon: ShieldOff };
}

export function jobDecisionMeta(decision?: string): StatusMeta {
  switch (decision?.toLowerCase()) {
    case "allow":
      return { label: "allow", tone: "success", shape: "square", icon: CheckCircle };
    case "deny":
      return { label: "deny", tone: "danger", shape: "square", icon: XCircle };
    case "require_approval":
      return { label: "approval", tone: "warning", shape: "shield", icon: Shield };
    case "throttle":
      return { label: "throttle", tone: "warning", shape: "square", icon: Clock };
    default:
      return { label: "none", tone: "muted", shape: "square", icon: Circle };
  }
}
