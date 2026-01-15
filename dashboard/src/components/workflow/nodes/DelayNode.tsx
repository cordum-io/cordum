import { memo } from "react";
import { Handle, Position, type NodeProps } from "reactflow";
import type { DelayNodeData } from "../types";

function formatDelay(seconds?: number): string {
  if (!seconds) return "Not set";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}

function DelayNodeComponent({ id, data, selected }: NodeProps<DelayNodeData>) {
  return (
    <div
      className={`builder-node builder-node--delay ${selected ? "builder-node--selected" : ""}`}
      onClick={() => data.onSelect(id)}
    >
      <Handle type="target" position={Position.Left} className="builder-handle" />

      <div className="builder-node__header">
        <div className="builder-node__icon bg-muted">DL</div>
        <div className="builder-node__info">
          <div className="builder-node__label">{data.label}</div>
          <div className="builder-node__type">Delay</div>
        </div>
        <button
          onClick={(e) => {
            e.stopPropagation();
            data.onDelete(id);
          }}
          className="builder-node__delete"
        >
          &times;
        </button>
      </div>

      <div className="builder-node__body">
        {data.delaySec && (
          <div className="builder-node__field">
            <span className="builder-node__field-label">Wait:</span>
            <span className="builder-node__field-value builder-node__field-value--mono">
              {formatDelay(data.delaySec)}
            </span>
          </div>
        )}
        {data.delayUntil && (
          <div className="builder-node__field">
            <span className="builder-node__field-label">Until:</span>
            <span className="builder-node__field-value builder-node__field-value--mono">
              {data.delayUntil}
            </span>
          </div>
        )}
        {!data.delaySec && !data.delayUntil && (
          <div className="builder-node__empty">
            No delay configured
          </div>
        )}
      </div>

      <Handle type="source" position={Position.Right} id="output" className="builder-handle" />
    </div>
  );
}

export const DelayNode = memo(DelayNodeComponent);
