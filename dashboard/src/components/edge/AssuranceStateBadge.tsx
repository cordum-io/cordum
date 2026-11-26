import type { EdgeAssuranceState } from "@/api/types";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";

// Renders the server's assurance_state as reported. It never derives a state
// from the policy mode, layers or status (task-9b412c07).
const ASSURANCE_DISPLAY: Record<EdgeAssuranceState, { label: string; variant: BadgeVariant }> = {
  "executor-enforced": { label: "Executor-enforced", variant: "healthy" },
  "hook-mediated": { label: "Hook-mediated", variant: "info" },
  compatibility: { label: "Compatibility", variant: "warning" },
  unsupported: { label: "Unsupported", variant: "danger" },
  stale: { label: "Stale", variant: "muted" },
};

const NOT_REPORTED = { label: "Not reported", variant: "muted" as const };

export const ASSURANCE_STATES = Object.keys(ASSURANCE_DISPLAY) as EdgeAssuranceState[];

export function AssuranceStateBadge({ state }: { state?: EdgeAssuranceState }) {
  const { label, variant } = state ? ASSURANCE_DISPLAY[state] : NOT_REPORTED;
  return (
    <span data-testid="edge-assurance-badge" data-assurance-state={state ?? "not-reported"} title="Assurance state">
      <StatusBadge variant={variant}>{label}</StatusBadge>
    </span>
  );
}
