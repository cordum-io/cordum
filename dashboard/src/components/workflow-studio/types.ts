import type { Node, Edge } from "reactflow";
import type { Workflow, WorkflowRun, RunStatus } from "@/api/types";

// ---------------------------------------------------------------------------
// Studio modes
// ---------------------------------------------------------------------------

export type StudioMode = "view" | "edit";

// ---------------------------------------------------------------------------
// Unified node data — used by UnifiedNode for both view and edit modes
// ---------------------------------------------------------------------------

export interface UnifiedNodeData {
  /** Display label */
  label: string;
  /** Original step ID */
  stepId: string;
  /** Step type (e.g. "job", "agent-task", "condition") */
  stepType: string;

  // --- Config fields (present in edit mode and blueprint view) ---
  topic?: string;
  condition?: string;
  worker_id?: string;
  for_each?: string;
  max_parallel?: number;
  input?: Record<string, unknown>;
  input_schema?: Record<string, unknown>;
  input_schema_id?: string;
  output_path?: string;
  output_schema?: Record<string, unknown>;
  output_schema_id?: string;
  meta?: Record<string, unknown>;
  on_error?: string;
  retry?: { max_retries?: number; backoff_sec?: number; backoff_multiplier?: number };
  timeout_sec?: number;
  delay_sec?: number;
  delay_until?: string;
  route_labels?: Record<string, string>;
  /** Legacy config bag */
  config?: Record<string, unknown>;

  // --- Run overlay fields (present when a run is selected) ---
  runStatus?: RunStatus;
  duration?: number;
  error?: string;
  safetyDecision?: { type: string };
  conditionResult?: boolean;

  // --- Mode awareness ---
  /** Current studio mode — nodes render differently based on this */
  mode: StudioMode;
}

// ---------------------------------------------------------------------------
// Graph data
// ---------------------------------------------------------------------------

export interface StudioGraphData {
  nodes: Node<UnifiedNodeData>[];
  edges: Edge[];
}

/** Imperative handle exposed by StudioCanvas for parent-driven graph updates */
export interface CanvasHandle {
  setNodes: React.Dispatch<React.SetStateAction<Node<UnifiedNodeData>[]>>;
  setEdges: React.Dispatch<React.SetStateAction<Edge[]>>;
  /** Read current graph state synchronously — safe to call in save callbacks */
  getGraph: () => { nodes: Node<UnifiedNodeData>[]; edges: Edge[] };
}

// ---------------------------------------------------------------------------
// Studio context — passed down from orchestrator
// ---------------------------------------------------------------------------

export interface StudioContext {
  mode: StudioMode;
  workflow: Workflow | null;
  run: WorkflowRun | null;
  isLoading: boolean;
  isSaving: boolean;
}
