import { useEffect, useState, useMemo } from "react";
import { X, Settings, AlertTriangle, ChevronDown, Zap } from "lucide-react";
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

// Preset options
const APPROVER_ROLES = [
  { value: "admin", label: "Admin" },
  { value: "reviewer", label: "Reviewer" },
  { value: "manager", label: "Manager" },
  { value: "security", label: "Security Team" },
  { value: "compliance", label: "Compliance" },
  { value: "owner", label: "Owner" },
];

const CONDITION_TEMPLATES = [
  { value: "{{ input.value == true }}", label: "Input is true" },
  { value: "{{ input.value == false }}", label: "Input is false" },
  { value: "{{ input.status == 'success' }}", label: "Status is success" },
  { value: "{{ input.status == 'failed' }}", label: "Status is failed" },
  { value: "{{ input.count > 0 }}", label: "Count greater than 0" },
  { value: "{{ input.result != null }}", label: "Result is not null" },
  { value: "{{ steps.prev.output.approved }}", label: "Previous step approved" },
  { value: "{{ env.ENVIRONMENT == 'production' }}", label: "Is production env" },
];

const DELAY_PRESETS = [
  { value: 30, label: "30s" },
  { value: 60, label: "1m" },
  { value: 300, label: "5m" },
  { value: 900, label: "15m" },
  { value: 1800, label: "30m" },
  { value: 3600, label: "1h" },
  { value: 86400, label: "24h" },
];

const FOREACH_TEMPLATES = [
  { value: "{{ input.items }}", label: "Input items array" },
  { value: "{{ input.users }}", label: "Input users array" },
  { value: "{{ input.files }}", label: "Input files array" },
  { value: "{{ steps.prev.output.results }}", label: "Previous step results" },
  { value: "{{ range(1, input.count) }}", label: "Range from 1 to count" },
];

const TIMEOUT_PRESETS = [
  { value: 60, label: "1m" },
  { value: 300, label: "5m" },
  { value: 600, label: "10m" },
  { value: 900, label: "15m" },
  { value: 1800, label: "30m" },
  { value: 3600, label: "1h" },
];

export function NodeConfigPanel({ node, onUpdate, onClose }: Props) {
  const [localData, setLocalData] = useState<Partial<BuilderNodeData>>({});

  // Fetch workflows for subworkflow selector
  const workflowsQuery = useQuery({
    queryKey: ["workflows"],
    queryFn: () => api.listWorkflows(),
    enabled: node?.data.nodeType === "subworkflow",
  });

  // Fetch packs for topic/capability selectors
  const packsQuery = useQuery({
    queryKey: ["packs"],
    queryFn: () => api.listPacks(),
    enabled: node?.data.nodeType === "worker",
  });

  // Derive topics and capabilities from packs
  const { packOptions, allTopics, capabilitiesForPack } = useMemo(() => {
    const packs = packsQuery.data?.items || [];
    const packOpts = packs.map((p) => ({
      value: p.id,
      label: p.manifest?.metadata?.title || p.id,
    }));

    // Collect all topics across packs
    const topicSet = new Set<string>();
    const capMap = new Map<string, string[]>();

    packs.forEach((pack) => {
      // Topics from pack manifest (it's an array)
      if (pack.manifest?.topics) {
        pack.manifest.topics.forEach((t) => {
          if (t.name) topicSet.add(t.name);
        });
      }
      // Capabilities from topics
      const caps: string[] = [];
      if (pack.manifest?.topics) {
        pack.manifest.topics.forEach((t) => {
          if (t.capability) caps.push(t.capability);
        });
      }
      capMap.set(pack.id, caps);
    });

    const topics = Array.from(topicSet).sort().map((t) => ({ value: t, label: t }));

    return {
      packOptions: packOpts,
      allTopics: topics,
      capabilitiesForPack: capMap,
    };
  }, [packsQuery.data]);

  // Get capabilities for selected pack
  const selectedPackId = (localData as { packId?: string }).packId;
  const availableCapabilities = useMemo(() => {
    if (!selectedPackId) return [];
    const caps = capabilitiesForPack.get(selectedPackId) || [];
    return caps.map((c) => ({ value: c, label: c }));
  }, [selectedPackId, capabilitiesForPack]);

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
              <div className="node-config-panel__combo">
                <Input
                  value={(localData as { topic?: string }).topic || ""}
                  onChange={(e) => handleChange("topic", e.target.value)}
                  placeholder="job.default"
                  list="topic-options"
                />
                <datalist id="topic-options">
                  {allTopics.map((t) => (
                    <option key={t.value} value={t.value} />
                  ))}
                </datalist>
                {allTopics.length > 0 && (
                  <select
                    className="node-config-panel__dropdown-trigger"
                    value=""
                    onChange={(e) => {
                      if (e.target.value) handleChange("topic", e.target.value);
                    }}
                  >
                    <option value="">▾</option>
                    {allTopics.map((t) => (
                      <option key={t.value} value={t.value}>{t.label}</option>
                    ))}
                  </select>
                )}
              </div>
              {packsQuery.isLoading && (
                <div className="node-config-panel__hint">Loading topics from packs...</div>
              )}
            </div>

            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Pack ID</label>
              <select
                value={(localData as { packId?: string }).packId || ""}
                onChange={(e) => handleChange("packId", e.target.value)}
                className="node-config-panel__select"
              >
                <option value="">Select a pack (optional)...</option>
                {packOptions.map((p: { value: string; label: string }) => (
                  <option key={p.value} value={p.value}>{p.label}</option>
                ))}
              </select>
            </div>

            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Capability</label>
              {availableCapabilities.length > 0 ? (
                <select
                  value={(localData as { capability?: string }).capability || ""}
                  onChange={(e) => handleChange("capability", e.target.value)}
                  className="node-config-panel__select"
                >
                  <option value="">Select a capability...</option>
                  {availableCapabilities.map((c) => (
                    <option key={c.value} value={c.value}>{c.label}</option>
                  ))}
                </select>
              ) : (
                <Input
                  value={(localData as { capability?: string }).capability || ""}
                  onChange={(e) => handleChange("capability", e.target.value)}
                  placeholder={selectedPackId ? "No capabilities in pack" : "Select a pack first"}
                />
              )}
            </div>

            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Timeout (seconds)</label>
              <div className="node-config-panel__input-with-presets">
                <Input
                  type="number"
                  value={(localData as { timeoutSec?: number }).timeoutSec || ""}
                  onChange={(e) => handleChange("timeoutSec", parseInt(e.target.value) || undefined)}
                  placeholder="300"
                />
                <div className="node-config-panel__presets">
                  {TIMEOUT_PRESETS.map((p) => (
                    <button
                      key={p.value}
                      type="button"
                      className="node-config-panel__preset-btn"
                      onClick={() => handleChange("timeoutSec", p.value)}
                    >
                      {p.label}
                    </button>
                  ))}
                </div>
              </div>
            </div>

            <div className="node-config-panel__divider" />

            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Retry - Max Retries</label>
              <div className="node-config-panel__input-with-presets">
                <Input
                  type="number"
                  value={(localData as { retry?: { maxRetries?: number } }).retry?.maxRetries || ""}
                  onChange={(e) => handleNestedChange("retry", "maxRetries", parseInt(e.target.value) || undefined)}
                  placeholder="3"
                />
                <div className="node-config-panel__presets">
                  {[1, 2, 3, 5].map((n) => (
                    <button
                      key={n}
                      type="button"
                      className="node-config-panel__preset-btn"
                      onClick={() => handleNestedChange("retry", "maxRetries", n)}
                    >
                      {n}
                    </button>
                  ))}
                </div>
              </div>
            </div>
          </>
        )}

        {/* Approval fields */}
        {nodeType === "approval" && (
          <>
            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Approver Role</label>
              <select
                value={(localData as { approverRole?: string }).approverRole || ""}
                onChange={(e) => handleChange("approverRole", e.target.value)}
                className="node-config-panel__select"
              >
                <option value="">Select a role...</option>
                {APPROVER_ROLES.map((r) => (
                  <option key={r.value} value={r.value}>{r.label}</option>
                ))}
              </select>
              <div className="node-config-panel__hint">
                Or enter a custom role below
              </div>
              <Input
                value={(localData as { approverRole?: string }).approverRole || ""}
                onChange={(e) => handleChange("approverRole", e.target.value)}
                placeholder="Custom role..."
                className="mt-2"
              />
            </div>

            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Approval Policy</label>
              <select
                value={(localData as { approvalPolicy?: string }).approvalPolicy || ""}
                onChange={(e) => handleChange("approvalPolicy", e.target.value)}
                className="node-config-panel__select"
              >
                <option value="">Default policy</option>
                <option value="any">Any approver</option>
                <option value="all">All approvers</option>
                <option value="majority">Majority vote</option>
                <option value="escalate">Escalate to manager</option>
              </select>
            </div>
          </>
        )}

        {/* Condition fields */}
        {nodeType === "condition" && (
          <>
            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Condition Expression</label>
              <select
                value=""
                onChange={(e) => {
                  if (e.target.value) handleChange("condition", e.target.value);
                }}
                className="node-config-panel__select mb-2"
              >
                <option value="">Choose a template...</option>
                {CONDITION_TEMPLATES.map((t) => (
                  <option key={t.value} value={t.value}>{t.label}</option>
                ))}
              </select>
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
              <div className="node-config-panel__presets mt-2">
                {DELAY_PRESETS.map((p) => (
                  <button
                    key={p.value}
                    type="button"
                    className="node-config-panel__preset-btn"
                    onClick={() => handleChange("delaySec", p.value)}
                  >
                    {p.label}
                  </button>
                ))}
              </div>
            </div>

            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Delay Until</label>
              <Input
                type="datetime-local"
                value={(localData as { delayUntil?: string }).delayUntil || ""}
                onChange={(e) => handleChange("delayUntil", e.target.value)}
              />
              <div className="node-config-panel__hint">
                Alternative to delay seconds - wait until specific time
              </div>
            </div>
          </>
        )}

        {/* Loop fields */}
        {nodeType === "loop" && (
          <>
            <div className="node-config-panel__section">
              <label className="node-config-panel__label">ForEach Expression</label>
              <select
                value=""
                onChange={(e) => {
                  if (e.target.value) handleChange("forEach", e.target.value);
                }}
                className="node-config-panel__select mb-2"
              >
                <option value="">Choose a template...</option>
                {FOREACH_TEMPLATES.map((t) => (
                  <option key={t.value} value={t.value}>{t.label}</option>
                ))}
              </select>
              <Input
                value={(localData as { forEach?: string }).forEach || ""}
                onChange={(e) => handleChange("forEach", e.target.value)}
                placeholder="{{ input.items }}"
              />
              <div className="node-config-panel__hint">
                Expression that yields an array to iterate over
              </div>
            </div>

            <div className="node-config-panel__section">
              <label className="node-config-panel__label">Max Parallel</label>
              <div className="node-config-panel__input-with-presets">
                <Input
                  type="number"
                  value={(localData as { maxParallel?: number }).maxParallel || ""}
                  onChange={(e) => handleChange("maxParallel", parseInt(e.target.value) || undefined)}
                  placeholder="1"
                />
                <div className="node-config-panel__presets">
                  {[1, 2, 5, 10].map((n) => (
                    <button
                      key={n}
                      type="button"
                      className="node-config-panel__preset-btn"
                      onClick={() => handleChange("maxParallel", n)}
                    >
                      {n}
                    </button>
                  ))}
                </div>
              </div>
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
              {workflowsQuery.isLoading && (
                <div className="node-config-panel__hint">Loading workflows...</div>
              )}
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
