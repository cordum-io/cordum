import { useMemo, useState } from "react";
import { cn } from "../../lib/utils";
import { useMemory } from "../../hooks/useMemory";
import { useToastStore } from "../../state/toast";
import { MemoryPanel } from "./MemoryPanel";
import { ArtifactPanel } from "./ArtifactPanel";
import type { Job, SafetyDecision, MemoryPayload, MemoryEntryRole } from "../../api/types";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type SidebarTab = "input" | "output" | "artifacts" | "memory" | "raw";

export interface ContextSidebarProps {
  job: Job;
  decisions?: SafetyDecision[] | null;
  defaultTab?: SidebarTab;
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const TABS: SidebarTab[] = ["input", "output", "artifacts", "memory", "raw"];

const TAB_LABELS: Record<SidebarTab, string> = {
  input: "Input",
  output: "Output",
  artifacts: "Artifacts",
  memory: "Memory",
  raw: "Raw",
};

const ROLE_COLORS: Record<MemoryEntryRole | string, string> = {
  system: "bg-gray-500/20 text-gray-400",
  user: "bg-blue-500/20 text-blue-400",
  assistant: "bg-[hsl(var(--accent))]/20 text-[hsl(var(--accent))]",
  agent: "bg-purple-500/20 text-purple-400",
  tool: "bg-amber-500/20 text-amber-400",
  unknown: "bg-gray-500/20 text-gray-400",
};

// ---------------------------------------------------------------------------
// Auto-select logic
// ---------------------------------------------------------------------------

function autoSelectTab(job: Job, defaultTab?: SidebarTab): SidebarTab {
  if (defaultTab) return defaultTab;
  switch (job.status) {
    case "running":
    case "approval_required":
    case "denied":
    case "failed":
      return "input";
    case "succeeded":
      return "output";
    case "output_quarantined":
      return "output";
    default:
      return job.contextPtr ? "input" : "raw";
  }
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function MemoryContentView({ data }: { data: MemoryPayload }) {
  // Chat-style entries
  if (data.entries && data.entries.length > 0) {
    return (
      <div className="space-y-3 max-h-96 overflow-y-auto">
        {data.entries.map((entry) => (
          <div key={entry.id} className="space-y-1">
            <div className="flex items-center gap-2">
              <span
                className={cn(
                  "rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase",
                  ROLE_COLORS[entry.role] ?? ROLE_COLORS.unknown,
                )}
              >
                {entry.role}
              </span>
              {entry.timestamp && (
                <span className="font-mono text-[10px] text-muted-foreground">
                  {new Date(entry.timestamp).toLocaleTimeString()}
                </span>
              )}
            </div>
            <p className="whitespace-pre-wrap text-xs text-ink">{entry.content}</p>
          </div>
        ))}
      </div>
    );
  }

  // Structured JSON
  if (data.json !== undefined && data.json !== null) {
    return (
      <pre className="max-h-80 overflow-auto rounded bg-surface2 p-3 font-mono text-xs text-ink">
        {JSON.stringify(data.json, null, 2)}
      </pre>
    );
  }

  // Plain text
  if (data.text) {
    return (
      <pre className="max-h-80 overflow-auto rounded bg-surface2 p-3 font-mono text-xs text-ink whitespace-pre-wrap">
        {data.text}
      </pre>
    );
  }

  return <p className="py-4 text-center text-xs text-muted-foreground">No content available</p>;
}

function InputTab({ job, active }: { job: Job; active: boolean }) {
  const { data, isLoading } = useMemory(active ? job.contextPtr : undefined);

  if (!job.contextPtr) {
    return <p className="py-4 text-center text-xs text-muted-foreground">No input context recorded</p>;
  }
  if (isLoading) {
    return <p className="py-4 text-center text-xs text-muted-foreground">Loading...</p>;
  }
  if (!data) {
    return <p className="py-4 text-center text-xs text-muted-foreground">No input context recorded</p>;
  }
  return <MemoryContentView data={data} />;
}

function OutputTab({ job, active }: { job: Job; active: boolean }) {
  const isQuarantined = job.status === "output_quarantined";
  const isRunning = job.status === "running" || job.status === "dispatched" || job.status === "scheduled";

  // For quarantined jobs, show the redacted output
  const ptr = isQuarantined
    ? (job.output_safety?.redacted_ptr ?? job.resultPtr)
    : job.resultPtr;

  const { data, isLoading } = useMemory(active ? ptr : undefined);

  if (isRunning) {
    return <p className="py-4 text-center text-xs text-muted-foreground">Job still in progress</p>;
  }
  if (!ptr) {
    return <p className="py-4 text-center text-xs text-muted-foreground">No output recorded</p>;
  }
  if (isLoading) {
    return <p className="py-4 text-center text-xs text-muted-foreground">Loading...</p>;
  }

  return (
    <div className="space-y-3">
      {isQuarantined && (
        <div className="rounded border border-amber-500/30 bg-amber-500/10 px-3 py-2">
          <p className="text-xs font-medium text-amber-400">
            This output was quarantined — showing redacted version
          </p>
        </div>
      )}
      {data ? <MemoryContentView data={data} /> : (
        <p className="py-4 text-center text-xs text-muted-foreground">No output available</p>
      )}
    </div>
  );
}

function RawTab({ job, decisions }: { job: Job; decisions?: SafetyDecision[] | null }) {
  const combined = useMemo(
    () => JSON.stringify({ job, safetyDecisions: decisions ?? [] }, null, 2),
    [job, decisions],
  );

  function handleCopy() {
    navigator.clipboard.writeText(combined).then(() => {
      useToastStore.getState().addToast({ type: "success", title: "JSON copied to clipboard" });
    });
  }

  return (
    <div className="space-y-2">
      <div className="flex justify-end">
        <button
          type="button"
          onClick={handleCopy}
          className={cn(
            "rounded border border-border px-2 py-1 text-[10px] font-medium text-muted-foreground",
            "transition-colors hover:border-[hsl(var(--accent))] hover:text-[hsl(var(--accent))]",
          )}
        >
          Copy JSON
        </button>
      </div>
      <pre className="max-h-[28rem] overflow-auto rounded bg-surface2 p-3 font-mono text-xs text-ink">
        {combined}
      </pre>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export function ContextSidebar({ job, decisions, defaultTab }: ContextSidebarProps) {
  const initialTab = useMemo(() => autoSelectTab(job, defaultTab), [job, defaultTab]);
  const [activeTab, setActiveTab] = useState<SidebarTab>(initialTab);

  return (
    <div className="lg:sticky lg:top-20 lg:max-h-[calc(100vh-8rem)] lg:overflow-y-auto space-y-4">
      {/* Tab bar */}
      <div className="flex gap-1 rounded-lg bg-surface1 p-1" role="tablist">
        {TABS.map((tab) => (
          <button
            key={tab}
            type="button"
            role="tab"
            aria-selected={activeTab === tab}
            onClick={() => setActiveTab(tab)}
            className={cn(
              "rounded-md px-3 py-1.5 text-xs font-medium transition-colors",
              activeTab === tab
                ? "bg-surface2 text-ink shadow-sm"
                : "text-muted-foreground hover:text-ink",
            )}
          >
            {TAB_LABELS[tab]}
          </button>
        ))}
      </div>

      {/* Tab content */}
      <div className="rounded border border-border bg-surface1 p-4">
        {activeTab === "input" && <InputTab job={job} active={activeTab === "input"} />}
        {activeTab === "output" && <OutputTab job={job} active={activeTab === "output"} />}
        {activeTab === "artifacts" && (
          <div className="max-w-full overflow-hidden">
            <ArtifactPanel jobId={job.id} />
          </div>
        )}
        {activeTab === "memory" && (
          job.contextPtr
            ? <MemoryPanel memoryPtr={job.contextPtr} jobId={job.id} />
            : <p className="py-4 text-center text-xs text-muted-foreground">No memory data available</p>
        )}
        {activeTab === "raw" && <RawTab job={job} decisions={decisions} />}
      </div>
    </div>
  );
}
