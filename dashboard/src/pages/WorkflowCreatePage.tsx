/*
 * DESIGN: "Control Surface" — Workflow Builder
 * PRD Section 12: Visual drag-and-drop workflow builder
 */
import { useState, useCallback, useRef, useEffect } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { motion } from "framer-motion";
import { Button } from "@/components/ui/Button";
import { StatusBadge } from "@/components/ui/StatusBadge";
import {
  ArrowLeft, Save, Rocket, X, Plus, Briefcase, Shield, GitBranch,
  Clock, Repeat, Layers, Workflow,
  AlertTriangle, Trash2,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { toast } from "sonner";
import { useCreateWorkflow, useWorkflow, useWorkflows } from "@/hooks/useWorkflows";
import { usePolicyBundles } from "@/hooks/usePolicies";
import type { WorkflowStep } from "@/api/types";

type NodeType = "worker" | "approval" | "condition" | "delay" | "loop" | "parallel" | "subworkflow" | "unknown";

interface BuilderNode {
  id: string;
  type: NodeType;
  label: string;
  x: number;
  y: number;
  config: Record<string, string | number | boolean>;
}

interface BuilderEdge {
  id: string;
  source: string;
  target: string;
  label?: string;
}

/** Read a config value as a string (safe accessor for Record<string, string | number | boolean>). */
function cfgStr(config: BuilderNode["config"], key: string, fallback = ""): string {
  const v = config[key];
  return v != null ? String(v) : fallback;
}

/** Read a config value as a number (safe accessor for Record<string, string | number | boolean>). */
function cfgNum(config: BuilderNode["config"], key: string, fallback = 0): number {
  const v = config[key];
  return v != null ? Number(v) : fallback;
}

interface NodeTypeMeta { type: NodeType; label: string; icon: LucideIcon; color: string; desc: string }

/**
 * BUILDER RESILIENCE CHECKLIST — Adding a new step type:
 * 1. Add entry here in NODE_TYPES with type, label, icon, color, desc
 * 2. Add type alias mapping in normalizeStepType() if backend name differs
 * 3. Add type mapping in transform.ts normalizeWorkflowNodeType() (WORKFLOW_NODE_TYPES set + switch logic)
 * 4. Add reverse mapping in useWorkflows.ts buildStepPayload() JOB_SUBTYPES or frontendType logic
 * 5. Add config panel in the right-panel JSX below (selectedNodeData.type === "yourType")
 * 6. Add test case in useWorkflows.test.ts resolveNodeMeta + normalizeStepType tests
 * 7. Verify: resolveNodeMeta() always returns UNKNOWN_NODE_META for unrecognized types (never undefined)
 * 8. Verify: deploy/save serialization restores originalType for round-trip fidelity
 */
const NODE_TYPES: NodeTypeMeta[] = [
  { type: "worker", label: "Worker", icon: Briefcase, color: "text-cordum border-cordum/30", desc: "Execute a job" },
  { type: "approval", label: "Approval", icon: Shield, color: "text-[var(--color-warning)] border-[var(--color-warning)]/30", desc: "Human gate" },
  { type: "condition", label: "Condition", icon: GitBranch, color: "text-[var(--color-info)] border-[var(--color-info)]/30", desc: "Branch logic" },
  { type: "delay", label: "Delay", icon: Clock, color: "text-muted-foreground border-muted-foreground/30", desc: "Wait duration" },
  { type: "loop", label: "Loop", icon: Repeat, color: "text-[var(--color-info)] border-[var(--color-info)]/30", desc: "Iterate items" },
  { type: "parallel", label: "Parallel", icon: Layers, color: "text-cordum border-cordum/30", desc: "Concurrent" },
  { type: "subworkflow", label: "Subworkflow", icon: Workflow, color: "text-muted-foreground border-muted-foreground/30", desc: "Nested flow" },
];

const UNKNOWN_NODE_META: NodeTypeMeta = {
  type: "unknown", label: "Unknown", icon: AlertTriangle, color: "text-destructive border-destructive/30", desc: "Unsupported step type",
};

/** Safe lookup — never returns undefined. Preserves original backend type in node.config._originalType. */
export function resolveNodeMeta(type: string): NodeTypeMeta {
  return NODE_TYPES.find(t => t.type === type) ?? UNKNOWN_NODE_META;
}

/** Normalize backend step type to a known NodeType, preserving the original for serialization. */
export function normalizeStepType(backendType: string): { nodeType: NodeType; originalType: string } {
  const mapped = backendType === "job" ? "worker"
    : backendType === "sub-workflow" ? "subworkflow"
    : backendType;
  const meta = NODE_TYPES.find(t => t.type === mapped);
  return { nodeType: meta ? meta.type : "unknown", originalType: backendType };
}

export default function WorkflowBuilderPage() {
  const nodeCounterRef = useRef(0);
  const navigate = useNavigate();
  const { id } = useParams();
  const isEdit = !!id;

  const createWorkflow = useCreateWorkflow();
  const { data: existingWorkflow } = useWorkflow(isEdit ? id : null);
  const { data: workflowsData } = useWorkflows();
  const { data: bundlesData } = usePolicyBundles();
  const workflows = workflowsData ?? [];
  const bundles = bundlesData?.items ?? [];

  const [workflowName, setWorkflowName] = useState("");
  const [nodes, setNodes] = useState<BuilderNode[]>([]);
  const [edges, setEdges] = useState<BuilderEdge[]>([]);
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [dragging, setDragging] = useState<string | null>(null);
  const [connecting, setConnecting] = useState<string | null>(null);
  const [dragOffset, setDragOffset] = useState({ x: 0, y: 0 });
  const canvasRef = useRef<HTMLDivElement>(null);
  const editLoaded = useRef(false);

  useEffect(() => {
    if (!existingWorkflow || editLoaded.current) return;
    editLoaded.current = true;
    setWorkflowName(existingWorkflow.name ?? "");
    // Defensive: steps may arrive as a backend object (map) if the transform didn't run
    const rawSteps = existingWorkflow.steps;
    const stepsArr: WorkflowStep[] = Array.isArray(rawSteps)
      ? rawSteps
      : rawSteps && typeof rawSteps === "object"
        ? Object.entries(rawSteps).map(([id, s]: [string, any]) => ({ id, name: s?.name ?? id, type: s?.type ?? "job", depends_on: s?.depends_on, config: s?.config }))
        : [];
    const loadedNodes: BuilderNode[] = stepsArr.map((s, i) => {
      nodeCounterRef.current = Math.max(nodeCounterRef.current, i + 1);
      const { nodeType, originalType } = normalizeStepType(s.type ?? "unknown");
      return {
        id: s.id ?? `node-${i + 1}`,
        type: nodeType,
        label: s.name ?? s.id ?? `Step ${i + 1}`,
        x: 200 + (i % 4) * 180,
        y: 100 + Math.floor(i / 4) * 120,
        config: { ...(s.config ?? {}), _originalType: originalType } as Record<string, string | number | boolean>,
      };
    });
    setNodes(loadedNodes);
    const loadedEdges: BuilderEdge[] = [];
    for (const s of stepsArr) {
      for (const dep of s.depends_on ?? []) {
        loadedEdges.push({ id: `edge-${dep}-${s.id}`, source: dep, target: s.id ?? "" });
      }
    }
    setEdges(loadedEdges);
  }, [existingWorkflow]);

  const handleCanvasMouseMove = useCallback((e: React.MouseEvent) => {
    if (!dragging || !canvasRef.current) return;
    const rect = canvasRef.current.getBoundingClientRect();
    const x = e.clientX - rect.left - dragOffset.x + canvasRef.current.scrollLeft;
    const y = e.clientY - rect.top - dragOffset.y + canvasRef.current.scrollTop;
    setNodes(prev => prev.map(n => n.id === dragging ? { ...n, x: Math.max(0, x), y: Math.max(0, y) } : n));
  }, [dragging, dragOffset]);

  const handleCanvasMouseUp = useCallback(() => {
    setDragging(null);
  }, []);

  const startDrag = useCallback((nodeId: string, e: React.MouseEvent) => {
    e.stopPropagation();
    const node = nodes.find(n => n.id === nodeId);
    if (!node || !canvasRef.current) return;
    const rect = canvasRef.current.getBoundingClientRect();
    setDragOffset({
      x: e.clientX - rect.left - node.x + canvasRef.current.scrollLeft,
      y: e.clientY - rect.top - node.y + canvasRef.current.scrollTop,
    });
    setDragging(nodeId);
  }, [nodes]);

  const handleNodeClick = useCallback((nodeId: string, e: React.MouseEvent) => {
    e.stopPropagation();
    if (connecting) {
      if (connecting !== nodeId && !edges.find(ed => ed.source === connecting && ed.target === nodeId)) {
        setEdges(prev => [...prev, { id: `edge-${connecting}-${nodeId}`, source: connecting, target: nodeId }]);
      }
      setConnecting(null);
    } else {
      setSelectedNode(nodeId);
    }
  }, [connecting, edges]);

  const startConnect = useCallback((nodeId: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setConnecting(nodeId);
    toast.info("Click another node to connect");
  }, []);

  const addNode = useCallback((type: NodeType) => {
    nodeCounterRef.current++;
    const count = nodeCounterRef.current;
    const newNode: BuilderNode = {
      id: `node-${count}`,
      type,
      label: `${type.charAt(0).toUpperCase() + type.slice(1)} ${count}`,
      x: 200 + (count % 4) * 180,
      y: 100 + Math.floor(count / 4) * 120,
      config: {},
    };
    setNodes(prev => [...prev, newNode]);
    setSelectedNode(newNode.id);
  }, []);

  const removeNode = useCallback((nodeId: string) => {
    setNodes(prev => prev.filter(n => n.id !== nodeId));
    setEdges(prev => prev.filter(e => e.source !== nodeId && e.target !== nodeId));
    if (selectedNode === nodeId) setSelectedNode(null);
  }, [selectedNode]);

  const updateNodeLabel = useCallback((nodeId: string, label: string) => {
    setNodes(prev => prev.map(n => n.id === nodeId ? { ...n, label } : n));
  }, []);

  const updateNodeConfig = useCallback((nodeId: string, key: string, value: string | number | boolean) => {
    setNodes(prev => prev.map(n => n.id === nodeId ? { ...n, config: { ...n.config, [key]: value } } : n));
  }, []);

  const selectedNodeData = nodes.find(n => n.id === selectedNode);
  const nodeTypeInfo = selectedNodeData ? resolveNodeMeta(selectedNodeData.type) : null;

  const handleDeploy = () => {
    if (!workflowName.trim()) {
      toast.error("Workflow name is required");
      return;
    }
    if (nodes.length === 0) {
      toast.error("Add at least one node to the workflow");
      return;
    }
    const steps: WorkflowStep[] = nodes.map(node => {
      const { _originalType, ...cleanConfig } = node.config as Record<string, unknown>;
      const stepType = node.type === "unknown" && typeof _originalType === "string"
        ? _originalType
        : node.type === "worker" ? "job" : node.type === "subworkflow" ? "sub-workflow" : node.type;
      return { id: node.id, name: node.label, type: stepType, depends_on: edges.filter(e => e.target === node.id).map(e => e.source), config: cleanConfig as Record<string, string | number | boolean> };
    });
    createWorkflow.mutate(
      { name: workflowName, steps },
      { onSuccess: (data) => navigate(data?.id ? `/workflows/${data.id}` : "/workflows") },
    );
  };

  return (
    <div className="h-[calc(100vh-64px)] flex flex-col -m-6">
      {/* Top Bar */}
      <div className="flex items-center justify-between px-5 py-3 border-b border-border bg-surface-0 shrink-0">
        <div className="flex items-center gap-3">
          <button onClick={() => navigate("/workflows")} className="p-1.5 rounded-full hover:bg-surface-2 transition-colors">
            <ArrowLeft className="w-4 h-4 text-muted-foreground" />
          </button>
          <input
            type="text"
            value={workflowName}
            onChange={(e) => setWorkflowName(e.target.value)}
            placeholder="Workflow name..."
            className="text-sm font-display font-semibold bg-transparent border-none outline-none text-foreground placeholder:text-muted-foreground w-64"
          />
          <StatusBadge variant="info">Draft</StatusBadge>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" onClick={() => navigate("/workflows")}>Cancel</Button>
          <Button variant="outline" size="sm" loading={createWorkflow.isPending} onClick={() => {
            if (!workflowName.trim()) { toast.error("Workflow name is required"); return; }
            const steps: WorkflowStep[] = nodes.map(node => {
              const { _originalType, ...cleanConfig } = node.config as Record<string, unknown>;
              const stepType = node.type === "unknown" && typeof _originalType === "string"
                ? _originalType
                : node.type === "worker" ? "job" : node.type === "subworkflow" ? "sub-workflow" : node.type;
              return { id: node.id, name: node.label, type: stepType, depends_on: edges.filter(e => e.target === node.id).map(e => e.source), config: cleanConfig as Record<string, string | number | boolean> };
            });
            createWorkflow.mutate(
              { name: workflowName, steps },
              {
                onSuccess: () => toast.success("Draft saved"),
                onError: () => toast.error("Failed to save draft"),
              },
            );
          }}><Save className="w-3 h-3 mr-1" />Save Draft</Button>
          <Button variant="primary" size="sm" onClick={handleDeploy} loading={createWorkflow.isPending}><Rocket className="w-3 h-3 mr-1" />Deploy</Button>
        </div>
      </div>

      {/* 3-Panel Layout */}
      <div className="flex flex-1 overflow-hidden">
        {/* Left Sidebar — Node Types */}
        <div className="w-60 border-r border-border bg-surface-0 overflow-y-auto shrink-0">
          <div className="p-4">
            <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider mb-3">Node Types</p>
            <div className="space-y-1.5">
              {NODE_TYPES.map((nt) => {
                const Icon = nt.icon;
                return (
                  <button
                    key={nt.type}
                    onClick={() => addNode(nt.type)}
                    className="w-full flex items-center gap-3 px-3 py-2.5 rounded-2xl hover:bg-surface-1 transition-colors text-left group"
                  >
                    <div className={cn("w-8 h-8 rounded-2xl border flex items-center justify-center", nt.color)}>
                      <Icon className="w-4 h-4" />
                    </div>
                    <div className="flex-1 min-w-0">
                      <p className="text-xs font-medium text-foreground">{nt.label}</p>
                      <p className="text-[10px] text-muted-foreground">{nt.desc}</p>
                    </div>
                    <Plus className="w-3 h-3 text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity" />
                  </button>
                );
              })}
            </div>
          </div>
          <div className="border-t border-border p-4">
            <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider mb-3">Packs</p>
            <div className="space-y-1">
              {["slack.send", "github.create-pr", "jira.create-issue", "email.send"].map((pack) => (
                <button
                  key={pack}
                  onClick={() => {
                    addNode("worker");
                    toast.info(`Added worker node with topic: job.${pack}`);
                  }}
                  className="w-full text-left px-3 py-2 rounded-2xl hover:bg-surface-1 transition-colors"
                >
                  <span className="text-xs font-mono text-cordum">job.{pack}</span>
                </button>
              ))}
            </div>
          </div>
        </div>

        {/* Canvas */}
        <div
          ref={canvasRef}
          className="flex-1 relative overflow-auto dot-grid"
          onClick={() => { setSelectedNode(null); setConnecting(null); }}
          onMouseMove={handleCanvasMouseMove}
          onMouseUp={handleCanvasMouseUp}
        >
          {nodes.length === 0 && (
            <div className="absolute inset-0 flex items-center justify-center">
              <div className="text-center">
                <Workflow className="w-12 h-12 text-muted-foreground/30 mx-auto mb-3" />
                <p className="text-sm text-muted-foreground">Drag nodes from the sidebar or click to add</p>
                <p className="text-xs text-muted-foreground/60 mt-1">Connect nodes to build your workflow</p>
              </div>
            </div>
          )}

          {/* Render edges as SVG lines */}
          <svg className="absolute inset-0 w-full h-full pointer-events-none">
            {edges.map((edge) => {
              const src = nodes.find(n => n.id === edge.source);
              const tgt = nodes.find(n => n.id === edge.target);
              if (!src || !tgt) return null;
              return (
                <line
                  key={edge.id}
                  x1={src.x + 80} y1={src.y + 30}
                  x2={tgt.x + 80} y2={tgt.y + 30}
                  stroke="rgba(0,229,160,0.3)"
                  strokeWidth={2}
                  strokeDasharray="6 4"
                />
              );
            })}
          </svg>

          {/* Render nodes */}
          {nodes.map((node) => {
            const nt = resolveNodeMeta(node.type);
            const Icon = nt.icon;
            const isSelected = selectedNode === node.id;
            return (
              <motion.div
                key={node.id}
                initial={{ opacity: 0, scale: 0.9 }}
                animate={{ opacity: 1, scale: 1 }}
                style={{ position: "absolute", left: node.x, top: node.y }}
                onMouseDown={(e) => startDrag(node.id, e)}
                onClick={(e) => handleNodeClick(node.id, e)}
                className={cn(
                  "w-40 rounded-2xl border bg-surface-1 shadow-lg cursor-pointer transition-all",
                  isSelected ? "ring-2 ring-cordum border-cordum/40" : "border-border hover:border-cordum/20",
                )}
              >
                <div className={cn("h-1 rounded-t-2xl", nt.color.includes("cordum") ? "bg-cordum" : nt.color.includes("warning") ? "bg-[var(--color-warning)]" : nt.color.includes("info") ? "bg-[var(--color-info)]" : "bg-muted-foreground")} />
                <div className="p-3">
                  <div className="flex items-center gap-2 mb-1">
                    <Icon className={cn("w-3.5 h-3.5", nt.color.split(" ")[0])} />
                    <span className="text-xs font-medium text-foreground truncate">{node.label}</span>
                  </div>
                  <p className="text-[10px] text-muted-foreground">{nt.desc}</p>
                  {/* Policy badge — shows which bundle governs this step */}
                  {node.type === "worker" && (
                    <div className="mt-1.5 flex items-center gap-1">
                      <Shield className="w-2.5 h-2.5 text-cordum" />
                      <span className="text-[9px] font-mono text-cordum">{cfgStr(node.config, "policyBundle") && cfgStr(node.config, "policyBundle") !== "none" ? cfgStr(node.config, "policyBundle") : "no policy"}</span>
                    </div>
                  )}
                  {node.type === "approval" && (
                    <div className="mt-1.5 flex items-center gap-1">
                      <Shield className="w-2.5 h-2.5 text-[var(--color-warning)]" />
                      <span className="text-[9px] font-mono text-[var(--color-warning)]">human-gate</span>
                    </div>
                  )}
                  <button
                    onClick={(e) => startConnect(node.id, e)}
                    className="mt-1.5 w-full text-[9px] font-mono text-muted-foreground hover:text-cordum bg-surface-2 rounded px-2 py-0.5 transition-colors"
                  >
                    Connect &rarr;
                  </button>
                </div>
              </motion.div>
            );
          })}
        </div>

        {/* Right Panel — Node Config */}
        {selectedNodeData && nodeTypeInfo && (
          <motion.div
            initial={{ x: 320 }}
            animate={{ x: 0 }}
            transition={{ type: "spring", stiffness: 300, damping: 30 }}
            className="w-80 border-l border-border bg-surface-0 overflow-y-auto shrink-0"
          >
            <div className="p-4 border-b border-border flex items-center justify-between">
              <div className="flex items-center gap-2">
                <nodeTypeInfo.icon className={cn("w-4 h-4", nodeTypeInfo.color.split(" ")[0])} />
                <span className="text-sm font-display font-semibold text-foreground">{nodeTypeInfo.label} Config</span>
              </div>
              <button onClick={() => setSelectedNode(null)} className="p-1 rounded hover:bg-surface-2 transition-colors">
                <X className="w-3.5 h-3.5 text-muted-foreground" />
              </button>
            </div>
            <div className="p-4 space-y-4">
              <div>
                <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Label</label>
                <input
                  type="text"
                  value={selectedNodeData.label}
                  onChange={(e) => updateNodeLabel(selectedNodeData.id, e.target.value)}
                  className="h-8 w-full px-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum"
                />
              </div>

              {selectedNodeData.type === "worker" && (
                <>
                  <div>
                    <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Topic</label>
                    <input type="text" placeholder="e.g., service.restart" value={cfgStr(selectedNodeData.config, "topic")} onChange={(e) => updateNodeConfig(selectedNodeData.id, "topic", e.target.value)} className="h-8 w-full px-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum" />
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Timeout</label>
                      <input type="number" value={cfgNum(selectedNodeData.config, "timeout", 30)} onChange={(e) => updateNodeConfig(selectedNodeData.id, "timeout", Number(e.target.value))} className="h-8 w-full px-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum" />
                    </div>
                    <div>
                      <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Retries</label>
                      <input type="number" value={cfgNum(selectedNodeData.config, "retries")} min={0} max={5} onChange={(e) => updateNodeConfig(selectedNodeData.id, "retries", Number(e.target.value))} className="h-8 w-full px-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum" />
                    </div>
                  </div>
                  {/* Policy Binding */}
                  <div>
                    <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5 flex items-center gap-1">
                      <Shield className="w-3 h-3 text-cordum" /> Policy Bundle
                    </label>
                    <select value={cfgStr(selectedNodeData.config, "policyBundle", "none")} onChange={(e) => updateNodeConfig(selectedNodeData.id, "policyBundle", e.target.value)} className="h-8 w-full px-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum">
                      <option value="none">No policy</option>
                      {bundles.map((b) => (
                        <option key={b.id} value={b.id}>{b.name || b.id}</option>
                      ))}
                    </select>
                    <p className="text-[9px] text-muted-foreground mt-1">Safety Kernel evaluates this bundle before dispatch</p>
                  </div>
                </>
              )}

              {selectedNodeData.type === "approval" && (
                <>
                  <div>
                    <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Approvers</label>
                    <input type="text" placeholder="admin, ops-team" value={cfgStr(selectedNodeData.config, "approvers")} onChange={(e) => updateNodeConfig(selectedNodeData.id, "approvers", e.target.value)} className="h-8 w-full px-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum" />
                  </div>
                  <div>
                    <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Message</label>
                    <textarea rows={3} placeholder="Message shown to approver..." value={cfgStr(selectedNodeData.config, "message")} onChange={(e) => updateNodeConfig(selectedNodeData.id, "message", e.target.value)} className="w-full px-3 py-2 text-xs bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum resize-none" />
                  </div>
                </>
              )}

              {selectedNodeData.type === "condition" && (
                <div>
                  <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Expression</label>
                  <textarea rows={3} placeholder="ctx.risk_score > 0.8" value={cfgStr(selectedNodeData.config, "expression")} onChange={(e) => updateNodeConfig(selectedNodeData.id, "expression", e.target.value)} className="w-full px-3 py-2 text-xs font-mono bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum resize-none" />
                </div>
              )}

              {selectedNodeData.type === "delay" && (
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Duration</label>
                    <input type="number" value={cfgNum(selectedNodeData.config, "duration", 60)} onChange={(e) => updateNodeConfig(selectedNodeData.id, "duration", Number(e.target.value))} className="h-8 w-full px-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum" />
                  </div>
                  <div>
                    <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Unit</label>
                    <select value={cfgStr(selectedNodeData.config, "durationUnit", "seconds")} onChange={(e) => updateNodeConfig(selectedNodeData.id, "durationUnit", e.target.value)} className="h-8 w-full px-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum">
                      <option>seconds</option><option>minutes</option><option>hours</option>
                    </select>
                  </div>
                </div>
              )}

              {selectedNodeData.type === "loop" && (
                <>
                  <div>
                    <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Items Expression</label>
                    <textarea rows={2} placeholder="ctx.items" value={cfgStr(selectedNodeData.config, "itemsExpr")} onChange={(e) => updateNodeConfig(selectedNodeData.id, "itemsExpr", e.target.value)} className="w-full px-3 py-2 text-xs font-mono bg-surface-1 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-cordum resize-none" />
                  </div>
                  <div>
                    <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Max Iterations</label>
                    <input type="number" value={cfgNum(selectedNodeData.config, "maxIterations", 100)} onChange={(e) => updateNodeConfig(selectedNodeData.id, "maxIterations", Number(e.target.value))} className="h-8 w-full px-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum" />
                  </div>
                </>
              )}

              {selectedNodeData.type === "parallel" && (
                <>
                  <div>
                    <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Branches</label>
                    <input type="number" value={cfgNum(selectedNodeData.config, "branches", 2)} min={2} max={10} onChange={(e) => updateNodeConfig(selectedNodeData.id, "branches", Number(e.target.value))} className="h-8 w-full px-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum" />
                  </div>
                  <div className="flex items-center justify-between">
                    <label className="text-xs text-foreground">Wait for all</label>
                    <div className="w-9 h-5 rounded-full bg-cordum/20 relative cursor-pointer">
                      <div className="absolute left-0.5 top-0.5 w-4 h-4 rounded-full bg-cordum transition-transform" />
                    </div>
                  </div>
                </>
              )}

              {selectedNodeData.type === "subworkflow" && (
                <div>
                  <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1.5">Workflow</label>
                  <select value={cfgStr(selectedNodeData.config, "subWorkflowId")} onChange={(e) => updateNodeConfig(selectedNodeData.id, "subWorkflowId", e.target.value)} className="h-8 w-full px-3 text-xs bg-surface-1 border border-border rounded-2xl text-foreground focus:outline-none focus:ring-1 focus:ring-cordum">
                    <option value="">Select workflow...</option>
                    {workflows.map((w) => (
                      <option key={w.id} value={w.id}>{w.name || w.id}</option>
                    ))}
                    {workflows.length === 0 && <option disabled>No workflows available</option>}
                  </select>
                </div>
              )}

              <div className="pt-3 border-t border-border">
                <Button variant="danger" size="sm" className="w-full" onClick={() => removeNode(selectedNodeData.id)}>
                  <Trash2 className="w-3 h-3 mr-1" />
                  Remove Node
                </Button>
              </div>
            </div>
          </motion.div>
        )}
      </div>
    </div>
  );
}
