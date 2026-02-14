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

type IconComponent = ComponentType<SVGProps<SVGSVGElement> & { className?: string }>;

export interface StatusMeta {
  label: string;
  tone: "success" | "warning" | "danger" | "info" | "muted" | "accent";
  shape: "circle" | "diamond" | "square" | "shield" | "triangle";
  icon: IconComponent;
}

export function runStatusMeta(status?: string): StatusMeta {
  switch (status) {
    case "succeeded":
    case "completed":
      return { label: status, tone: "success", shape: "circle", icon: CheckCircle };
    case "waiting":
      return { label: status, tone: "warning", shape: "circle", icon: Clock };
    case "running":
    case "in_progress":
      return { label: status, tone: "accent", shape: "circle", icon: Loader };
    case "failed":
    case "timed_out":
      return { label: status, tone: "danger", shape: "circle", icon: XCircle };
    case "pending":
    case "queued":
      return { label: status, tone: "warning", shape: "circle", icon: Clock };
    case "cancelled":
      return { label: status, tone: "muted", shape: "circle", icon: XCircle };
    default:
      return { label: status ?? "unknown", tone: "muted", shape: "circle", icon: Circle };
  }
}

export function jobStatusMeta(state?: string): StatusMeta {
  switch (state) {
    case "succeeded":
      return { label: state, tone: "success", shape: "diamond", icon: CheckCircle };
    case "running":
    case "dispatched":
      return { label: state, tone: "accent", shape: "diamond", icon: Loader };
    case "scheduled":
      return { label: state, tone: "info", shape: "diamond", icon: Clock };
    case "approval_required":
      return { label: "approval", tone: "warning", shape: "shield", icon: Shield };
    case "output_quarantined":
      return { label: "quarantined", tone: "warning", shape: "shield", icon: AlertTriangle };
    case "failed":
    case "denied":
    case "timeout":
      return { label: state, tone: "danger", shape: "diamond", icon: XCircle };
    case "pending":
      return { label: state, tone: "warning", shape: "diamond", icon: Clock };
    case "cancelled":
      return { label: state, tone: "muted", shape: "diamond", icon: XCircle };
    default:
      return { label: state ?? "unknown", tone: "muted", shape: "diamond", icon: Circle };
  }
}

export function approvalStatusMeta(required?: boolean): StatusMeta {
  if (required) {
    return { label: "approval required", tone: "warning", shape: "shield", icon: Shield };
  }
  return { label: "no approval", tone: "muted", shape: "shield", icon: ShieldOff };
}
