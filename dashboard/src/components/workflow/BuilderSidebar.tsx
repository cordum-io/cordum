import type { DragEvent } from "react";
import {
  MessageSquare,
  Package,
  Wrench,
  ShieldCheck,
  Clock,
  GitBranch,
  Bell,
  Split,
  Layers,
  Globe,
  Code,
  GitMerge,
  Repeat,
  Workflow,
  AlertTriangle,
} from "lucide-react";
import { Input } from "../ui/Input";
import { Textarea } from "../ui/Textarea";

// ---------------------------------------------------------------------------
// Node palette items
// ---------------------------------------------------------------------------

const AGENT_NODES = [
  { type: "agent-task", label: "Agent Task", icon: MessageSquare, color: "text-[var(--color-info)]" },
  { type: "pack-action", label: "Pack Action", icon: Package, color: "text-primary" },
  { type: "tool-call", label: "Tool Call", icon: Wrench, color: "text-[var(--color-warning)]" },
] as const;

const FLOW_NODES = [
  { type: "approval", label: "Approval", icon: ShieldCheck, color: "text-[var(--color-warning)]" },
  { type: "delay", label: "Delay", icon: Clock, color: "text-primary" },
  { type: "condition", label: "Condition", icon: GitBranch, color: "text-[var(--color-info)]" },
  { type: "notify", label: "Notify", icon: Bell, color: "text-primary" },
  { type: "fan-out", label: "Fan-out", icon: Split, color: "text-primary" },
  { type: "parallel", label: "Parallel", icon: Layers, color: "text-[var(--color-info)]" },
  { type: "http", label: "HTTP", icon: Globe, color: "text-primary" },
  { type: "transform", label: "Transform", icon: Code, color: "text-primary" },
  { type: "switch", label: "Switch", icon: GitMerge, color: "text-[var(--color-info)]" },
  { type: "loop", label: "Loop", icon: Repeat, color: "text-[var(--color-warning)]" },
  { type: "sub-workflow", label: "Sub-flow", icon: Workflow, color: "text-[var(--color-info)]" },
  { type: "error-trigger", label: "Error", icon: AlertTriangle, color: "text-destructive" },
] as const;

function onDragStart(event: DragEvent, nodeType: string) {
  event.dataTransfer.setData("application/reactflow", nodeType);
  event.dataTransfer.effectAllowed = "move";
}

// ---------------------------------------------------------------------------
// Sidebar
// ---------------------------------------------------------------------------

export interface BuilderSidebarProps {
  name: string;
  description: string;
  onNameChange: (name: string) => void;
  onDescriptionChange: (description: string) => void;
}

export function BuilderSidebar({
  name,
  description,
  onNameChange,
  onDescriptionChange,
}: BuilderSidebarProps) {
  return (
    <aside className="flex w-60 shrink-0 flex-col gap-6 border-r border-border bg-surface1 p-4 overflow-y-auto">
      {/* Node palette */}
      <section>
        <h3 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Node Types
        </h3>

        <h4 className="mb-2 text-[10px] font-semibold uppercase tracking-wider text-muted/60">
          Agent &amp; Actions
        </h4>
        <div className="grid grid-cols-2 gap-2">
          {AGENT_NODES.map(({ type, label, icon: Icon, color }) => (
            <div
              key={type}
              draggable
              onDragStart={(e) => onDragStart(e, type)}
              className="flex cursor-grab flex-col items-center gap-1.5 rounded-xl border border-border bg-card/60 p-3 text-xs font-medium text-ink shadow-sm transition-all hover:border-accent hover:shadow-soft active:cursor-grabbing"
            >
              <Icon className={`h-5 w-5 ${color}`} />
              {label}
            </div>
          ))}
        </div>

        <h4 className="mb-2 mt-3 text-[10px] font-semibold uppercase tracking-wider text-muted/60">
          Flow Control
        </h4>
        <div className="grid grid-cols-2 gap-2">
          {FLOW_NODES.map(({ type, label, icon: Icon, color }) => (
            <div
              key={type}
              draggable
              onDragStart={(e) => onDragStart(e, type)}
              className="flex cursor-grab flex-col items-center gap-1.5 rounded-xl border border-border bg-card/60 p-3 text-xs font-medium text-ink shadow-sm transition-all hover:border-accent hover:shadow-soft active:cursor-grabbing"
            >
              <Icon className={`h-5 w-5 ${color}`} />
              {label}
            </div>
          ))}
        </div>
      </section>

      {/* Workflow metadata */}
      <section>
        <h3 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Workflow
        </h3>
        <div className="space-y-3">
          <div>
            <label className="mb-1 block text-xs text-muted-foreground">Name</label>
            <Input
              value={name}
              onChange={(e) => onNameChange(e.target.value)}
              placeholder="My workflow"
            />
          </div>
          <div>
            <label className="mb-1 block text-xs text-muted-foreground">Description</label>
            <Textarea
              value={description}
              onChange={(e) => onDescriptionChange(e.target.value)}
              placeholder="What does this workflow do?"
              rows={3}
            />
          </div>
        </div>
      </section>
    </aside>
  );
}
