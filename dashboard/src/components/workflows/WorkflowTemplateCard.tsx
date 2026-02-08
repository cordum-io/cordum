import { useNavigate } from "react-router-dom";
import {
  Play,
  Pencil,
  Briefcase,
  GitBranch,
  Clock,
  UserCheck,
  Bell,
  GitFork,
} from "lucide-react";
import type { Workflow } from "../../api/types";
import { useWorkflowStats } from "../../hooks/useWorkflows";
import { RunStatusBadge } from "../StatusBadge";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { cn } from "../../lib/utils";
import type { RunStatus } from "../../api/types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function timeAgo(iso?: string | null): string {
  if (!iso) return "\u2014";
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1_000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

function truncate(str: string, max: number): string {
  return str.length > max ? str.slice(0, max) + "\u2026" : str;
}

const STEP_TYPE_ICONS: Record<string, typeof Briefcase> = {
  job: Briefcase,
  condition: GitBranch,
  delay: Clock,
  approval: UserCheck,
  notify: Bell,
  fan_out: GitFork,
  fanout: GitFork,
};

const TRIGGER_VARIANTS: Record<string, "info" | "success" | "warning" | "default"> = {
  manual: "default",
  scheduled: "info",
  event: "success",
  webhook: "warning",
};

// ---------------------------------------------------------------------------
// Inline SuccessSparkline (until dedicated component exists)
// ---------------------------------------------------------------------------

const SPARKLINE_COLORS: Record<string, string> = {
  succeeded: "bg-green-500",
  completed: "bg-green-500",
  failed: "bg-red-500",
  timed_out: "bg-red-500",
  cancelled: "bg-gray-400",
  running: "bg-blue-500",
  in_progress: "bg-blue-500",
  pending: "bg-gray-300",
  waiting: "bg-amber-500",
};

function SuccessSparkline({ data }: { data: RunStatus[] }) {
  if (data.length === 0) return null;
  return (
    <div className="flex items-end gap-px h-3">
      {data.map((status, i) => (
        <div
          key={i}
          className={cn(
            "w-1 rounded-sm",
            SPARKLINE_COLORS[status] ?? "bg-gray-200",
            status === "succeeded" || status === "completed" ? "h-3" : "h-2",
          )}
        />
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Step type mini-icons
// ---------------------------------------------------------------------------

function StepTypeIcons({ steps }: { steps: Workflow["steps"] }) {
  const MAX_SHOWN = 8;
  const shown = steps.slice(0, MAX_SHOWN);
  const overflow = steps.length - MAX_SHOWN;

  return (
    <div className="flex items-center gap-0.5">
      {shown.map((step, i) => {
        const Icon = STEP_TYPE_ICONS[step.type] ?? Briefcase;
        return <Icon key={step.id ?? i} className="h-3 w-3 text-muted" />;
      })}
      {overflow > 0 && (
        <span className="text-[10px] text-muted">+{overflow}</span>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// WorkflowTemplateCard
// ---------------------------------------------------------------------------

export interface WorkflowTemplateCardProps {
  workflow: Workflow;
  onRunNow: (workflowId: string) => void;
}

export function WorkflowTemplateCard({ workflow, onRunNow }: WorkflowTemplateCardProps) {
  const navigate = useNavigate();
  const { data: stats } = useWorkflowStats(workflow.id);

  const triggerType = workflow.triggerType ?? "manual";
  const triggerVariant = TRIGGER_VARIANTS[triggerType.toLowerCase()] ?? "default";

  return (
    <Card
      className="cursor-pointer hover:shadow-lg transition-shadow"
      onClick={() => navigate(`/workflows/${workflow.id}`)}
    >
      {/* Top row: name + trigger badge + Run Now */}
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0 flex-1">
          <h3 className="text-sm font-semibold text-ink truncate">
            {truncate(workflow.name, 40)}
          </h3>
        </div>
        <div className="flex items-center gap-2 flex-shrink-0">
          <Badge variant={triggerVariant}>{triggerType}</Badge>
          <Button
            variant="ghost"
            size="sm"
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              navigate(`/workflows/${workflow.id}/edit`);
            }}
            title="Edit workflow"
          >
            <Pencil className="h-3 w-3" />
          </Button>
          <Button
            variant="outline"
            size="sm"
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              onRunNow(workflow.id);
            }}
          >
            <Play className="h-3 w-3" />
            Run
          </Button>
        </div>
      </div>

      {/* Description */}
      {workflow.description && (
        <p className="mt-1.5 text-sm text-muted line-clamp-2">
          {workflow.description}
        </p>
      )}

      {/* Stats row */}
      <div className="mt-3 flex flex-wrap items-center gap-4 text-xs text-muted">
        {/* Step count + type icons */}
        <span className="inline-flex items-center gap-1.5">
          {workflow.steps.length} step{workflow.steps.length !== 1 ? "s" : ""}
          <StepTypeIcons steps={workflow.steps} />
        </span>

        {/* Last run */}
        {stats?.lastRunStatus && (
          <span className="inline-flex items-center gap-1">
            {timeAgo(stats.lastRunTime)}
            <RunStatusBadge status={stats.lastRunStatus} />
          </span>
        )}

        {/* Success rate + sparkline */}
        {stats && stats.sparkline.length > 0 && (
          <span className="inline-flex items-center gap-1.5">
            {stats.successRate}%
            <SuccessSparkline data={stats.sparkline} />
          </span>
        )}
      </div>
    </Card>
  );
}
