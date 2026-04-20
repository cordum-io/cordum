// Unit coverage for the MCP approval hook shapes. The hooks themselves
// wrap React Query; we test the helpers and type shapes here — E2E
// interaction (render → click Approve → server call → list refresh) is
// covered by the integration tests in step 12.
import { describe, expect, it } from "vitest";
import { shortArgsHash, type McpApproval, type McpApprovalStatus } from "./useMcpApprovals";

describe("shortArgsHash", () => {
  it("truncates long hashes with an ellipsis", () => {
    expect(shortArgsHash("abcdefghij0123456789")).toBe("abcdefgh…");
  });

  it("returns the full value when already short", () => {
    expect(shortArgsHash("abc")).toBe("abc");
  });

  it("returns empty string for empty input", () => {
    expect(shortArgsHash("")).toBe("");
  });

  it("returns full value at the 10-char boundary", () => {
    expect(shortArgsHash("abcdefghij")).toBe("abcdefghij");
  });
});

describe("McpApproval type", () => {
  it("accepts a pending approval with the documented fields", () => {
    const rec: McpApproval = {
      id: "app-1",
      tenant: "default",
      agent_id: "agent-1",
      tool_name: "files.delete",
      args_hash: "deadbeef",
      status: "pending",
      created_at: 1,
      expires_at: 2,
    };
    expect(rec.id).toBe("app-1");
  });

  it("allows the documented status transitions", () => {
    const statuses: McpApprovalStatus[] = [
      "pending",
      "approved",
      "rejected",
      "expired",
      "invalidated",
    ];
    expect(statuses).toHaveLength(5);
  });
});
