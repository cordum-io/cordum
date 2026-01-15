import { useEffect, useState } from "react";
import { X, Settings, AlertTriangle } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { Input } from "../ui/Input";
import { Button } from "../ui/Button";
import type { BuilderNode, BuilderNodeData } from "./types";

type Props = {
  node: BuilderNode | null;
  onUpdate: (nodeId: string, data: Partial<BuilderNodeData>) => void;
  onClose: () => void;
};

export function NodeConfigPanel({ node, onUpdate, onClose }: Props) {
  const [localData, setLocalData] = useState<Partial<BuilderNodeData>>({});

  // Fetch workflows for subworkflow selector
  const workflowsQuery = useQuery({
    queryKey: ["workflows"],
    queryFn: () => api.listWorkflows(),
    enabled: node?.data.nodeType === "subworkflow",
  });

  useEffect(() => {
    if (node) {
      setLocalData({ ...node.data });
    }
  }, [node?.id]);

  if (!node) {
    return (
      <div className="node-config-panel node-config-panel--empty">
        <div className="node-config-panel__placeholder">
          <Settings className="h-8 w-8 text-muted" />
          <div className="text-sm text-muted mt-2">Select a node to configure</div>
        </div>
      </div>
    );
  }

  const handleChange = (key: string, value: unknown) => {
    const updated = { ...localData, [key]: value };
    setLocalData(updated);
    onUpdate(node.id, { [key]: value } as Partial<BuilderNodeData>);
  };

  const handleNestedChange = (parent: string, key: string, value: unknown) => {
    const parentObj = (localData as Record<string, unknown>)[parent] as Record<string, unknown> || {};
    const updated = { ...parentObj, [key]: value };
    handleChange(parent, updated);
  };

  const nodeType = node.data.nodeType;

  return (
    <div className="node-config-panel">
      <div className="node-config-panel__header">
        <div className="node-config-panel__title">
          <Settings className="h-4 w-4" />
          <span>Configure {node.data.label}</span>
        </div>
        <button onClick={onClose} className="node-config-panel__close">
          <X className="h-4 w-4" />
        </button>
      </div>

      <div className="node-config-panel__content">
        {/* Common fields */}
        <div className="node-config-panel__section">
          <label className="node-config-panel__label">Label</label>
          <Input
            value={localData.label || ""}
            onChange={(e) => handleChange("label", e.target.value)}
            placeholder="Step label"
          />
        </div>

        <div className="node-config-panel__section">
          <label className="node-config-panel__label">Description</label>
          <Input
            value={localData.description || ""}
            onChange={(e) => handleChange("description", e.target.value)}
            placeholder="Optional description"
          />
        </div>

        {/* Worker fields */}
        {nodeType === "worker" && (
          <>
            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Topic</label>
              <Input
                value={(localData as { topic?: string }).topic || ""}
                onChange={(e) => handleChange("topic", e.target.value)}
                placeholder="job.default"
              />
            </div>

            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Pack ID</label>
              <Input
                value={(localData as { packId?: string }).packId || ""}
                onChange={(e) => handleChange("packId", e.target.value)}
                placeholder="Optional pack ID"
              />
            </div>

            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Capability</label>
              <Input
                value={(localData as { capability?: string }).capability || ""}
                onChange={(e) => handleChange("capability", e.target.value)}
                placeholder="Optional capability"
              />
            </div>

            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Timeout (seconds)</label>
              <Input
                type="number"
                value={(localData as { timeoutSec?: number }).timeoutSec || ""}
                onChange={(e) => handleChange("timeoutSec", parseInt(e.target.value) || undefined)}
                placeholder="300"
              />
            </div>

            <div className="node-config-panel__divider" />

            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Retry - Max Retries</label>
              <Input
                type="number"
                value={(localData as { retry?: { maxRetries?: number } }).retry?.maxRetries || ""}
                onChange={(e) => handleNestedChange("retry", "maxRetries", parseInt(e.target.value) || undefined)}
                placeholder="3"
              />
            </div>
          </>
        )}

        {/* Approval fields */}
        {nodeType === "approval" && (
          <>
            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Approver Role</label>
              <Input
                value={(localData as { approverRole?: string }).approverRole || ""}
                onChange={(e) => handleChange("approverRole", e.target.value)}
                placeholder="admin, reviewer, etc."
              />
            </div>

            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Approval Policy</label>
              <Input
                value={(localData as { approvalPolicy?: string }).approvalPolicy || ""}
                onChange={(e) => handleChange("approvalPolicy", e.target.value)}
                placeholder="Policy reference"
              />
            </div>
          </>
        )}

        {/* Condition fields */}
        {nodeType === "condition" && (
          <>
            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Condition Expression</label>
              <Input
                value={(localData as { condition?: string }).condition || ""}
                onChange={(e) => handleChange("condition", e.target.value)}
                placeholder="{{ input.value == true }}"
              />
              <div className="node-config-panel__hint">
                Use template syntax to reference step outputs
              </div>
            </div>

            <div className="node-config-panel__info">
              <AlertTriangle className="h-4 w-4 text-warning" />
              <span>This node has two outputs: True and False</span>
            </div>
          </>
        )}

        {/* Delay fields */}
        {nodeType === "delay" && (
          <>
            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Delay (seconds)</label>
              <Input
                type="number"
                value={(localData as { delaySec?: number }).delaySec || ""}
                onChange={(e) => handleChange("delaySec", parseInt(e.target.value) || undefined)}
                placeholder="60"
              />
            </div>

            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Delay Until</label>
              <Input
                value={(localData as { delayUntil?: string }).delayUntil || ""}
                onChange={(e) => handleChange("delayUntil", e.target.value)}
                placeholder="ISO date or cron expression"
              />
              <div className="node-config-panel__hint">
                Alternative to delay seconds
              </div>
            </div>
          </>
        )}

        {/* Loop fields */}
        {nodeType === "loop" && (
          <>
            <div className="node-config-panel__section">
              <label className="node-config-panel__label">ForEach Expression</label>
              <Input
                value={(localData as { forEach?: string }).forEach || ""}
                onChange={(e) => handleChange("forEach", e.target.value)}
                placeholder="{{ input.items }}"
              />
              <div className="node-config-panel__hint">
                Expression that yields an array
              </div>
            </div>

            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Max Parallel</label>
              <Input
                type="number"
                value={(localData as { maxParallel?: number }).maxParallel || ""}
                onChange={(e) => handleChange("maxParallel", parseInt(e.target.value) || undefined)}
                placeholder="1"
              />
            </div>

            <div className="node-config-panel__info">
              <AlertTriangle className="h-4 w-4 text-warning" />
              <span>This node has two outputs: Body (per item) and Done (after all)</span>
            </div>
          </>
        )}

        {/* Parallel fields */}
        {nodeType === "parallel" && (
          <>
            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Wait for All</label>
              <select
                value={(localData as { waitAll?: boolean }).waitAll !== false ? "true" : "false"}
                onChange={(e) => handleChange("waitAll", e.target.value === "true")}
                className="node-config-panel__select"
              >
                <option value="true">Yes - Wait for all branches</option>
                <option value="false">No - Continue when first completes</option>
              </select>
            </div>

            <div className="node-config-panel__hint">
              Connect multiple nodes from this step to create parallel branches
            </div>
          </>
        )}

        {/* Subworkflow fields */}
        {nodeType === "subworkflow" && (
          <>
            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Subworkflow</label>
              <select
                value={(localData as { subworkflowId?: string }).subworkflowId || ""}
                onChange={(e) => handleChange("subworkflowId", e.target.value)}
                className="node-config-panel__select"
              >
                <option value="">Select a workflow...</option>
                {workflowsQuery.data?.map((wf) => (
                  <option key={wf.id} value={wf.id}>
                    {wf.name || wf.id}
                  </option>
                ))}
              </select>
            </div>
          </>
        )}
      </div>

      <div className="node-config-panel__footer">
        <Button variant="outline" size="sm" onClick={onClose}>
          Close
        </Button>
      </div>
    </div>
  );
}
