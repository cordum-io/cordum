import { useEffect, useMemo, useState, type ReactNode } from "react";
import ReactFlow, {
  Background,
  BackgroundVariant,
  Controls,
  MarkerType,
  type Edge,
  type Node,
  useEdgesState,
  useNodesState,
} from "reactflow";
import type { Step, Workflow, WorkflowRun } from "../../types/api";
import { Drawer } from "../ui/Drawer";
import { Badge } from "../ui/Badge";
import { formatDateTime, formatDuration } from "../../lib/format";
import { builderNodeTypes } from "./nodeTypes";
import type { BuilderNodeData, BuilderNodeType } from "./types";

const defaultEdgeOptions = {
  type: "smoothstep",
  markerEnd: { type: MarkerType.ArrowClosed, color: "#9aa7b0" },
  style: { stroke: "#9aa7b0", strokeWidth: 1.4 },
};

const builderNodeTypeSet = new Set<string>([
  "worker",
  "approval",
  "condition",
  "delay",
  "loop",
  "parallel",
  "subworkflow",
]);

const noop = () => {};

const hasValue = (value: ReactNode) => value !== undefined && value !== null && value !== "";

const isBuilderNodeType = (value?: string): value is BuilderNodeType => {
  if (!value) {
    return false;
  }
  return builderNodeTypeSet.has(value);
};

const mapStepTypeToNodeType = (stepType?: string): BuilderNodeType => {
  switch (stepType?.toLowerCase()) {
    case "approval":
      return "approval";
    case "condition":
    case "if":
      return "condition";
    case "delay":
    case "wait":
    case "timer":
      return "delay";
    case "loop":
    case "foreach":
    case "for_each":
      return "loop";
    case "parallel":
    case "fan_out":
      return "parallel";
    case "subworkflow":
    case "workflow":
    case "call":
      return "subworkflow";
    default:
      return "worker";
  }
};

const resolveNodeType = (workflowStep?: Workflow["steps"][string]): BuilderNodeType => {
  const override = workflowStep?.meta?.labels?.ui_node_type;
  if (isBuilderNodeType(override)) {
    return override;
  }
  return mapStepTypeToNodeType(workflowStep?.type);
};

const formatStatus = (status?: string) => (status ? status.replace(/_/g, " ") : "");

const statusVariant = (status?: string): "default" | "success" | "warning" | "danger" | "info" => {
  if (!status) {
    return "default";
  }
  const normalized = status.toLowerCase();
  if (["failed", "timed_out", "cancelled", "blocked", "denied"].includes(normalized)) {
    return "danger";
  }
  if (["running", "in_progress", "pending", "queued", "waiting"].includes(normalized)) {
    return "warning";
  }
  if (["succeeded", "success"].includes(normalized)) {
    return "success";
  }
  return "info";
};

const formatRetry = (retry?: Step["retry"]) => {
  if (!retry) {
    return undefined;
  }
  const parts: string[] = [];
  if (retry.max_retries !== undefined) {
    parts.push(`max ${retry.max_retries}`);
  }
  if (retry.initial_backoff_sec !== undefined) {
    parts.push(`initial ${retry.initial_backoff_sec}s`);
  }
  if (retry.max_backoff_sec !== undefined) {
    parts.push(`max backoff ${retry.max_backoff_sec}s`);
  }
  if (retry.multiplier !== undefined) {
    parts.push(`x${retry.multiplier}`);
  }
  return parts.length ? parts.join(" · ") : undefined;
};

const formatSeconds = (value?: number) => (value !== undefined ? `${value}s` : undefined);

const buildNodeData = (stepId: string, step: Workflow["steps"][string], status?: string): BuilderNodeData => {
  const nodeType = resolveNodeType(step);
  const shared = {
    stepId,
    label: step?.name || stepId,
    onDelete: noop,
    onSelect: noop,
    readOnly: true,
    status,
  };

  if (nodeType === "condition") {
    return {
      ...shared,
      nodeType: "condition",
      condition: step?.condition || "",
    };
  }

  if (nodeType === "approval") {
    return {
      ...shared,
      nodeType: "approval",
      approverRole: step?.meta?.actor_id,
      approvalPolicy: step?.meta?.labels?.["approval_policy"],
    };
  }

  if (nodeType === "delay") {
    return {
      ...shared,
      nodeType: "delay",
      delaySec: step?.delay_sec,
      delayUntil: step?.delay_until,
    };
  }

  if (nodeType === "loop") {
    return {
      ...shared,
      nodeType: "loop",
      forEach: step?.for_each || "",
      maxParallel: step?.max_parallel,
    };
  }

  if (nodeType === "subworkflow") {
    return {
      ...shared,
      nodeType: "subworkflow",
      subworkflowId: step?.id,
    };
  }

  if (nodeType === "parallel") {
    return {
      ...shared,
      nodeType: "parallel",
      branches: [],
      waitAll: true,
    };
  }

  return {
    ...shared,
    nodeType: "worker",
    topic: step?.topic,
    packId: step?.meta?.pack_id,
    capability: step?.meta?.capability,
    riskTags: step?.meta?.risk_tags,
    requires: step?.meta?.requires,
    timeoutSec: step?.timeout_sec,
    retry: step?.retry,
  };
};

const buildDag = (
  workflow?: Workflow,
  run?: WorkflowRun,
  positions?: Map<string, { x: number; y: number }>
) => {
  const steps = workflow?.steps || {};
  const levels: Record<string, number> = {};

  const computeLevel = (id: string): number => {
    if (levels[id] !== undefined) {
      return levels[id];
    }
    const deps = steps[id]?.depends_on || [];
    if (deps.length === 0) {
      levels[id] = 0;
      return 0;
    }
    const level = Math.max(...deps.map((dep) => computeLevel(dep))) + 1;
    levels[id] = level;
    return level;
  };

  Object.keys(steps).forEach((id) => computeLevel(id));
  const levelCounts: Record<number, number> = {};

  const nodes: Node<BuilderNodeData>[] = Object.keys(steps).map((id) => {
    const step = steps[id];
    const level = levels[id] ?? 0;
    const index = levelCounts[level] || 0;
    levelCounts[level] = index + 1;
    const status = run?.steps?.[id]?.status;
    const position = positions?.get(id) ?? { x: level * 280, y: index * 150 };
    return {
      id,
      type: resolveNodeType(step),
      data: buildNodeData(id, step, status),
      position,
    };
  });

  const edges: Edge[] = [];
  Object.entries(steps).forEach(([id, step]) => {
    step.depends_on?.forEach((dep) => {
      edges.push({ id: `${dep}-${id}`, source: dep, target: id });
    });
  });

  return { nodes, edges };
};

type WorkflowCanvasProps = {
  workflow?: Workflow;
  run?: WorkflowRun;
  height?: number;
};

export function WorkflowCanvas({ workflow, run, height = 420 }: WorkflowCanvasProps) {
  const [nodes, setNodes, onNodesChange] = useNodesState<BuilderNodeData>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);

  const stepKey = useMemo(() => {
    if (!workflow?.steps) {
      return "";
    }
    return Object.keys(workflow.steps).sort().join("|");
  }, [workflow?.steps]);

  useEffect(() => {
    if (!workflow || stepKey.length === 0) {
      setNodes([]);
      setEdges([]);
      setSelectedNodeId(null);
      return;
    }

    setNodes((current) => {
      const positions = new Map(current.map((node) => [node.id, node.position]));
      const { nodes: nextNodes, edges: nextEdges } = buildDag(workflow, undefined, positions);
      setEdges(nextEdges);
      return nextNodes;
    });
  }, [workflow?.id, stepKey, setEdges, setNodes]);

  useEffect(() => {
    setNodes((current) =>
      current.map((node) => {
        const status = run?.steps?.[node.id]?.status;
        if (node.data.status === status) {
          return node;
        }
        return { ...node, data: { ...node.data, status } };
      })
    );
  }, [run?.id, run?.updated_at, setNodes, stepKey]);

  useEffect(() => {
    if (selectedNodeId && !workflow?.steps?.[selectedNodeId]) {
      setSelectedNodeId(null);
    }
  }, [selectedNodeId, workflow?.steps]);

  const selectedStep = selectedNodeId ? workflow?.steps?.[selectedNodeId] : undefined;
  const selectedRunStep = selectedNodeId ? run?.steps?.[selectedNodeId] : undefined;
  const selectedStatus = selectedRunStep?.status;
  const selectedRetry = formatRetry(selectedStep?.retry);
  const selectedTimeout = formatSeconds(selectedStep?.timeout_sec);
  const selectedDelay = formatSeconds(selectedStep?.delay_sec);
  const selectedDuration = selectedRunStep?.started_at
    ? formatDuration(selectedRunStep.started_at, selectedRunStep.completed_at ?? null)
    : undefined;
  const hasPayload =
    selectedRunStep?.input !== undefined ||
    selectedRunStep?.output !== undefined ||
    selectedRunStep?.error !== undefined;

  const detailFields = (fields: Array<{ label: string; value?: ReactNode }>) => (
    <div className="grid gap-3 sm:grid-cols-2">
      {fields.filter((field) => hasValue(field.value)).map((field) => (
        <div key={field.label} className="rounded-xl border border-border bg-white/70 px-3 py-2">
          <div className="text-[11px] uppercase tracking-[0.18em] text-muted">{field.label}</div>
          <div className="text-sm text-ink">{field.value}</div>
        </div>
      ))}
    </div>
  );
  const hasFields = (fields: Array<{ label: string; value?: ReactNode }>) =>
    fields.some((field) => hasValue(field.value));

  const stepFields = [
    { label: "Step ID", value: selectedNodeId },
    { label: "Name", value: selectedStep?.name },
    { label: "Engine Type", value: selectedStep?.type },
    { label: "UI Node Type", value: selectedStep?.meta?.labels?.ui_node_type },
    { label: "Depends On", value: selectedStep?.depends_on?.join(", ") },
  ];

  const logicFields = [
    { label: "Condition", value: selectedStep?.condition },
    { label: "Delay", value: selectedDelay },
    { label: "Delay Until", value: selectedStep?.delay_until },
    { label: "For Each", value: selectedStep?.for_each },
    { label: "Max Parallel", value: selectedStep?.max_parallel },
  ];

  const workerFields = [
    { label: "Topic", value: selectedStep?.topic },
    { label: "Pack", value: selectedStep?.meta?.pack_id },
    { label: "Capability", value: selectedStep?.meta?.capability },
    { label: "Timeout", value: selectedTimeout },
    { label: "Retry", value: selectedRetry },
  ];

  const approvalFields = [
    { label: "Approver Role", value: selectedStep?.meta?.actor_id },
    { label: "Approval Policy", value: selectedStep?.meta?.labels?.approval_policy },
  ];

  const runFields = [
    { label: "Status", value: selectedStatus ? formatStatus(selectedStatus) : undefined },
    { label: "Job ID", value: selectedRunStep?.job_id },
    { label: "Attempts", value: selectedRunStep?.attempts },
    { label: "Started", value: selectedRunStep?.started_at ? formatDateTime(selectedRunStep.started_at) : undefined },
    { label: "Completed", value: selectedRunStep?.completed_at ? formatDateTime(selectedRunStep.completed_at) : undefined },
    { label: "Duration", value: selectedDuration },
  ];

  const hasWorkflowSteps = Boolean(workflow && Object.keys(workflow.steps || {}).length);

  if (!workflow || !hasWorkflowSteps) {
    return (
      <div className="workflow-canvas empty" style={{ height }}>
        <div className="workflow-canvas__empty">No steps to display.</div>
      </div>
    );
  }

  return (
    <div className="workflow-canvas" style={{ height }}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={builderNodeTypes}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        defaultEdgeOptions={defaultEdgeOptions}
        nodesDraggable
        nodesConnectable={false}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeClick={(_, node) => setSelectedNodeId(node.id)}
      >
        <Background variant={BackgroundVariant.Dots} gap={22} size={1} color="#d0d7dd" />
        <Controls position="bottom-left" />
      </ReactFlow>

      <Drawer open={Boolean(selectedNodeId)} onClose={() => setSelectedNodeId(null)} size="md">
        {selectedStep ? (
          <div className="space-y-4">
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Step Details</div>
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <h3 className="text-xl font-semibold text-ink">{selectedStep.name || selectedNodeId}</h3>
                <div className="text-xs text-muted">{selectedNodeId}</div>
              </div>
              {selectedStatus ? (
                <Badge variant={statusVariant(selectedStatus)}>{formatStatus(selectedStatus)}</Badge>
              ) : null}
            </div>

            <div className="rounded-2xl border border-border bg-white/70 p-4 space-y-3">
              <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Step</div>
              {detailFields(stepFields)}
            </div>

            {hasFields(logicFields) && (
              <div className="rounded-2xl border border-border bg-white/70 p-4 space-y-3">
                <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Logic</div>
                {detailFields(logicFields)}
              </div>
            )}

            {hasFields(workerFields) && (
              <div className="rounded-2xl border border-border bg-white/70 p-4 space-y-3">
                <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Worker</div>
                {detailFields(workerFields)}
              </div>
            )}

            {(selectedStep.meta?.risk_tags?.length || selectedStep.meta?.requires?.length) && (
              <div className="rounded-2xl border border-border bg-white/70 p-4 space-y-3">
                <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Tags</div>
                {selectedStep.meta?.risk_tags?.length ? (
                  <div>
                    <div className="text-[11px] uppercase tracking-[0.18em] text-muted mb-2">Risk Tags</div>
                    <div className="flex flex-wrap gap-2">
                      {selectedStep.meta.risk_tags.map((tag) => (
                        <Badge key={tag} variant="danger">
                          {tag}
                        </Badge>
                      ))}
                    </div>
                  </div>
                ) : null}
                {selectedStep.meta?.requires?.length ? (
                  <div>
                    <div className="text-[11px] uppercase tracking-[0.18em] text-muted mb-2">Requires</div>
                    <div className="flex flex-wrap gap-2">
                      {selectedStep.meta.requires.map((tag) => (
                        <Badge key={tag}>{tag}</Badge>
                      ))}
                    </div>
                  </div>
                ) : null}
              </div>
            )}

            {hasFields(approvalFields) && (
              <div className="rounded-2xl border border-border bg-white/70 p-4 space-y-3">
                <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Approval</div>
                {detailFields(approvalFields)}
              </div>
            )}

            {hasFields(runFields) && (
              <div className="rounded-2xl border border-border bg-white/70 p-4 space-y-3">
                <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Run</div>
                {detailFields(runFields)}
              </div>
            )}

            {hasPayload && (
              <div className="rounded-2xl border border-border bg-white/70 p-4 space-y-3">
                <div className="text-xs font-semibold uppercase tracking-[0.2em] text-muted">Payload</div>
                {selectedRunStep.input !== undefined ? (
                  <div>
                    <div className="text-[11px] uppercase tracking-[0.18em] text-muted">Input</div>
                    <pre className="mt-2 rounded-xl bg-white/70 p-2 text-[11px] text-ink">
                      {JSON.stringify(selectedRunStep.input, null, 2)}
                    </pre>
                  </div>
                ) : null}
                {selectedRunStep.output !== undefined ? (
                  <div>
                    <div className="text-[11px] uppercase tracking-[0.18em] text-muted">Output</div>
                    <pre className="mt-2 rounded-xl bg-white/70 p-2 text-[11px] text-ink">
                      {JSON.stringify(selectedRunStep.output, null, 2)}
                    </pre>
                  </div>
                ) : null}
                {selectedRunStep.error !== undefined ? (
                  <div>
                    <div className="text-[11px] uppercase tracking-[0.18em] text-muted">Error</div>
                    <pre className="mt-2 rounded-xl bg-white/70 p-2 text-[11px] text-ink">
                      {JSON.stringify(selectedRunStep.error, null, 2)}
                    </pre>
                  </div>
                ) : null}
              </div>
            )}
          </div>
        ) : (
          <div className="space-y-2">
            <div className="text-xs uppercase tracking-[0.2em] text-muted">Step Details</div>
            <div className="text-sm text-muted">No step details available.</div>
          </div>
        )}
      </Drawer>
    </div>
  );
}
