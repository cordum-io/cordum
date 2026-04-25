import { useCallback } from "react";
import { useNavigate } from "react-router-dom";
import {
  ArrowLeft,
  Save,
  Rocket,
  Play,
  Pencil,
  Eye,
  Trash2,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";
import type { Workflow, WorkflowRun } from "@/api/types";
import type { StudioMode } from "./types";
import { statusToBadgeVariant, statusToBadgeLabel } from "./nodeRegistry";

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface StudioToolbarProps {
  mode: StudioMode;
  workflow: Workflow | null;
  run: WorkflowRun | null;
  name: string;
  onNameChange: (name: string) => void;
  onModeChange: (mode: StudioMode) => void;
  onSave: () => void;
  onDeploy: () => void;
  onRun: () => void;
  onDelete?: () => void;
  isSaving: boolean;
  isRunning: boolean;
}

// ---------------------------------------------------------------------------
// StudioToolbar
// ---------------------------------------------------------------------------

export function StudioToolbar({
  mode,
  workflow,
  run,
  name,
  onNameChange,
  onModeChange,
  onSave,
  onDeploy,
  onRun,
  onDelete,
  isSaving,
  isRunning,
}: StudioToolbarProps) {
  const navigate = useNavigate();
  const isEdit = mode === "edit";
  const isNew = !workflow?.id;
  const runBadge = run ? { variant: statusToBadgeVariant(run.status), label: statusToBadgeLabel(run.status) } : null;

  const handleBack = useCallback(() => {
    navigate("/workflows");
  }, [navigate]);

  const handleToggleMode = useCallback(() => {
    if (!workflow?.id) return;
    onModeChange(isEdit ? "view" : "edit");
  }, [workflow, isEdit, onModeChange]);

  return (
    <div className="flex items-center justify-between px-4 py-2.5 border-b border-border bg-surface-0 shadow-sm shrink-0 min-h-[52px]">
      {/* Left section: back + name + badges */}
      <div className="flex items-center gap-3 min-w-0 flex-1 border-r border-border/40 pr-4 mr-4">
        <button
          type="button"
          onClick={handleBack}
          className="p-1.5 rounded-full hover:bg-surface-2 transition-colors shrink-0"
          title="Back to workflows"
        >
          <ArrowLeft className="w-4 h-4 text-muted-foreground" />
        </button>

        {isEdit ? (
          <input
            type="text"
            value={name}
            onChange={(e) => onNameChange(e.target.value)}
            placeholder="Workflow name..."
            className="text-base font-display font-semibold bg-transparent border-none outline-none text-foreground placeholder:text-muted-foreground/60 min-w-0 flex-1 max-w-[300px] focus:ring-0"
          />
        ) : (
          <h1 className="text-base font-display font-semibold text-foreground truncate max-w-[300px]">
            {name || "Untitled Workflow"}
          </h1>
        )}

        {/* Version badge */}
        {workflow?.version && (
          <StatusBadge variant="muted">v{workflow.version}</StatusBadge>
        )}

        {/* Run status badge */}
        {runBadge && (
          <StatusBadge variant={runBadge.variant} dot pulse={run?.status === "running"}>
            {runBadge.label}
          </StatusBadge>
        )}

        {/* Draft badge for new workflows */}
        {isNew && isEdit && (
          <StatusBadge variant="info" dot pulse>Draft</StatusBadge>
        )}
      </div>

      {/* Center: mode toggle */}
      {workflow?.id && (
        <div className="flex items-center bg-surface-1 border border-border rounded-full p-0.5 mx-4 shrink-0">
          <button
            type="button"
            onClick={() => onModeChange("view")}
            disabled={isSaving || isRunning}
            className={cn(
              "flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-medium transition-all duration-150",
              !isEdit
                ? "bg-[var(--primary)]/10 text-[var(--primary)] shadow-sm"
                : "text-muted-foreground hover:text-foreground",
              (isSaving || isRunning) && "opacity-50 cursor-not-allowed",
            )}
          >
            <Eye className="w-3 h-3" />
            View
          </button>
          <button
            type="button"
            onClick={() => onModeChange("edit")}
            disabled={isSaving || isRunning}
            className={cn(
              "flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-medium transition-all duration-150",
              isEdit
                ? "bg-[var(--primary)]/10 text-[var(--primary)] shadow-sm"
                : "text-muted-foreground hover:text-foreground",
              (isSaving || isRunning) && "opacity-50 cursor-not-allowed",
            )}
          >
            <Pencil className="w-3 h-3" />
            Edit
          </button>
        </div>
      )}

      {/* Right section: action buttons */}
      <div className="flex items-center gap-2 shrink-0">
        {isEdit ? (
          <>
            {onDelete && workflow?.id && (
              <Button variant="ghost" size="sm" onClick={onDelete} disabled={isSaving || isRunning} title="Delete workflow">
                <Trash2 className="w-3 h-3" />
              </Button>
            )}
            <Button
              variant="outline"
              size="sm"
              onClick={onSave}
              loading={isSaving}
              disabled={!name.trim()}
            >
              <Save className="w-3 h-3 mr-1" />
              Save
            </Button>
            <Button
              variant="primary"
              size="sm"
              onClick={onDeploy}
              loading={isSaving}
              disabled={!name.trim()}
            >
              <Rocket className="w-3 h-3 mr-1" />
              Deploy
            </Button>
          </>
        ) : (
          <>
            {workflow?.id && (
              <Button
                variant="primary"
                size="sm"
                onClick={onRun}
                loading={isRunning}
              >
                <Play className="w-3 h-3 mr-1" />
                Run
              </Button>
            )}
            <Button
              variant="ghost"
              size="sm"
              onClick={handleToggleMode}
              disabled={!workflow?.id}
            >
              <Pencil className="w-3 h-3 mr-1" />
              Edit
            </Button>
          </>
        )}
      </div>
    </div>
  );
}
