import type { DragEvent } from "react";
import {
  Briefcase,
  ShieldCheck,
  Clock,
  GitBranch,
  Bell,
  Split,
} from "lucide-react";
import { Input } from "../ui/Input";
import { Textarea } from "../ui/Textarea";

// ---------------------------------------------------------------------------
// Node palette items
// ---------------------------------------------------------------------------

const NODE_TYPES = [
  { type: "job", label: "Job", icon: Briefcase, color: "text-blue-500" },
  { type: "approval", label: "Approval", icon: ShieldCheck, color: "text-amber-500" },
  { type: "delay", label: "Delay", icon: Clock, color: "text-purple-500" },
  { type: "condition", label: "Condition", icon: GitBranch, color: "text-teal-500" },
  { type: "notify", label: "Notify", icon: Bell, color: "text-pink-500" },
  { type: "fan-out", label: "Fan-out", icon: Split, color: "text-indigo-500" },
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
        <h3 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted">
          Node Types
        </h3>
        <div className="grid grid-cols-2 gap-2">
          {NODE_TYPES.map(({ type, label, icon: Icon, color }) => (
            <div
              key={type}
              draggable
              onDragStart={(e) => onDragStart(e, type)}
              className="flex cursor-grab flex-col items-center gap-1.5 rounded-xl border border-border bg-white/60 p-3 text-xs font-medium text-ink shadow-sm transition-all hover:border-accent hover:shadow-soft active:cursor-grabbing"
            >
              <Icon className={`h-5 w-5 ${color}`} />
              {label}
            </div>
          ))}
        </div>
      </section>

      {/* Workflow metadata */}
      <section>
        <h3 className="mb-3 text-xs font-semibold uppercase tracking-wider text-muted">
          Workflow
        </h3>
        <div className="space-y-3">
          <div>
            <label className="mb-1 block text-xs text-muted">Name</label>
            <Input
              value={name}
              onChange={(e) => onNameChange(e.target.value)}
              placeholder="My workflow"
            />
          </div>
          <div>
            <label className="mb-1 block text-xs text-muted">Description</label>
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
