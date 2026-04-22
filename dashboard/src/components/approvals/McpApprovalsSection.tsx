// McpApprovalsSection — renders the MCP approval queue as a titled
// section on the ApprovalsPage. Kept as its own component so the main
// page file doesn't grow again; the page imports this and drops it in
// above/below the existing job-approval queue.
import { useMcpApprovals } from "../../hooks/useMcpApprovals";
import { McpApprovalCard } from "./McpApprovalCard";

interface Props {
  statusFilter?: "pending" | "approved" | "rejected" | "expired";
}

export function McpApprovalsSection({ statusFilter = "pending" }: Props) {
  const { data, isLoading, error } = useMcpApprovals(statusFilter);

  return (
    <section
      aria-labelledby="mcp-approvals-heading"
      className="space-y-3"
      data-testid="mcp-approvals-section"
    >
      <div className="flex items-baseline justify-between">
        <h2 id="mcp-approvals-heading" className="text-lg font-semibold">
          MCP tool calls
        </h2>
        <span className="text-xs text-gray-500">
          {data ? `${data.length} ${statusFilter}` : ""}
        </span>
      </div>
      {isLoading && (
        <div className="text-sm text-gray-500" data-testid="mcp-approvals-loading">
          Loading MCP approvals…
        </div>
      )}
      {error && (
        <div className="text-sm text-red-600" data-testid="mcp-approvals-error">
          Failed to load MCP approvals.
        </div>
      )}
      {!isLoading && !error && data && data.length === 0 && (
        <div className="text-sm text-gray-500" data-testid="mcp-approvals-empty">
          No {statusFilter} MCP approvals.
        </div>
      )}
      {data && data.length > 0 && (
        <div className="space-y-2">
          {data.map((approval) => (
            <McpApprovalCard key={approval.id} approval={approval} />
          ))}
        </div>
      )}
    </section>
  );
}
