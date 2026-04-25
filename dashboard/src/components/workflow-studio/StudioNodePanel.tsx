import { useCallback, useMemo } from "react";
import { motion, AnimatePresence } from "framer-motion";
import type { Node } from "reactflow";
import type { Workflow, WorkflowRun, WorkflowStep } from "@/api/types";
import { StudioConfigPanel } from "./StudioConfigPanel";
import { NodeDetailPanel } from "@/components/workflows/dag/NodeDetailPanel";
import type { UnifiedNodeData, StudioMode } from "./types";

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface StudioNodePanelProps {
  mode: StudioMode;
  selectedNode: Node<UnifiedNodeData> | null;
  workflow: Workflow | null;
  run: WorkflowRun | null;
  allNodes: Node<UnifiedNodeData>[];
  onClose: () => void;
  /** Edit mode: called when node config is saved */
  onNodeConfigSave?: (nodeId: string, data: { label: string; config: Record<string, unknown> }) => void;
  /** Edit mode: called when node is deleted */
  onNodeDelete?: (nodeId: string) => void;
}

// ---------------------------------------------------------------------------
// Resolve WorkflowStep from selected node (for detail panel)
// ---------------------------------------------------------------------------

function resolveStep(
  selectedNode: Node<UnifiedNodeData>,
  workflow: Workflow | null,
  run: WorkflowRun | null,
): WorkflowStep | null {
  const stepId = selectedNode.data.stepId;

  // Prefer run steps (they have status/output data)
  if (run?.steps) {
    const runStep = run.steps.find((s) => s.id === stepId);
    if (runStep) return runStep;
  }

  // Fall back to workflow definition steps
  if (workflow?.steps) {
    const defStep = workflow.steps.find((s) => s.id === stepId);
    if (defStep) return defStep;
  }

  // Construct step from node data (includes all fields the node carries)
  const d = selectedNode.data;
  return {
    id: d.stepId,
    name: d.label,
    type: d.stepType,
    topic: d.topic,
    condition: d.condition,
    worker_id: d.worker_id,
    for_each: d.for_each,
    max_parallel: d.max_parallel,
    input: d.input,
    input_schema: d.input_schema,
    input_schema_id: d.input_schema_id,
    output_path: d.output_path,
    output_schema: d.output_schema,
    output_schema_id: d.output_schema_id,
    meta: d.meta,
    on_error: d.on_error,
    retry: d.retry,
    timeout_sec: d.timeout_sec,
    delay_sec: d.delay_sec,
    delay_until: d.delay_until,
    route_labels: d.route_labels,
    config: d.config,
  };
}

// ---------------------------------------------------------------------------
// StudioNodePanel
// ---------------------------------------------------------------------------

export function StudioNodePanel({
  mode,
  selectedNode,
  workflow,
  run,
  allNodes,
  onClose,
  onNodeConfigSave,
  onNodeDelete,
}: StudioNodePanelProps) {
  const isEdit = mode === "edit";

  // Resolve step for detail panel
  const step = useMemo(
    () => (selectedNode ? resolveStep(selectedNode, workflow, run) : null),
    [selectedNode, workflow, run],
  );

  const handleConfigSave = useCallback(
    (nodeId: string, data: { label: string; config: Record<string, unknown> }) => {
      onNodeConfigSave?.(nodeId, data);
    },
    [onNodeConfigSave],
  );

  const handleDelete = useCallback(
    (nodeId: string) => {
      onNodeDelete?.(nodeId);
    },
    [onNodeDelete],
  );

  return (
    <AnimatePresence>
      {selectedNode && (
        <motion.div
          key="node-panel"
          initial={{ x: 320, opacity: 0 }}
          animate={{ x: 0, opacity: 1 }}
          exit={{ x: 320, opacity: 0 }}
          transition={{ type: "spring", stiffness: 300, damping: 30 }}
          className="w-80 border-l border-border bg-surface-0 overflow-y-auto shrink-0 flex flex-col"
        >
          {isEdit ? (
            <StudioConfigPanel
              node={selectedNode}
              onSave={handleConfigSave}
              onClose={onClose}
              onDelete={handleDelete}
              allNodes={allNodes}
            />
          ) : (
            <NodeDetailPanel
              step={step}
              run={run}
              onClose={onClose}
              inline
            />
          )}
        </motion.div>
      )}
    </AnimatePresence>
  );
}
