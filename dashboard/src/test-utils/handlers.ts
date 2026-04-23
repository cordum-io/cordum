import { http, HttpResponse } from "msw";

export const baseHandlers = [
  http.get("*/api/v1/approvals", () =>
    HttpResponse.json({ items: [], next_cursor: null }),
  ),
  http.get("*/api/v1/mcp/approvals", () =>
    HttpResponse.json({ items: [] }),
  ),
  http.get("*/api/v1/mcp/approvals/:id", ({ params }) =>
    HttpResponse.json({
      id: String(params.id),
      tenant: "default",
      agent_id: "agent-test",
      tool_name: "test.tool",
      args_hash: "hash-test",
      status: "pending",
      created_at: 0,
      expires_at: 0,
    }),
  ),
];