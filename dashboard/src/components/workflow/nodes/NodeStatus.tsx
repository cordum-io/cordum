import { memo } from "react";

type NodeStatusProps = {
  status?: string;
};

const statusLabel = (status?: string) => {
  if (!status) {
    return "ready";
  }
  return status.replace(/_/g, " ");
};

const statusKey = (status?: string) => (status ? status.toLowerCase() : "ready");

function NodeStatusComponent({ status }: NodeStatusProps) {
  const label = statusLabel(status);
  return (
    <div className="builder-node__status" data-status={statusKey(status)}>
      <span className="builder-node__status-dot" />
      <span className="builder-node__status-text">{label}</span>
    </div>
  );
}

export const NodeStatus = memo(NodeStatusComponent);
