import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { TenantMcpGovernanceSection } from "./TenantMcpGovernanceSection";

describe("TenantMcpGovernanceSection", () => {
  it("renders canonical MCP-home and precedence guidance", () => {
    const markup = renderToStaticMarkup(
      <TenantMcpGovernanceSection
        matrix={{
          allowServers: ["github"],
          denyServers: ["internal-admin"],
          allowTools: ["search_issues"],
          denyTools: ["delete_issue"],
          allowResources: ["repo:*"],
          denyResources: ["secret:*"],
          allowActions: ["read"],
          denyActions: ["delete"],
        }}
      />,
    );

    expect(markup).toContain("canonical home for MCP allow/deny governance");
    expect(markup).toContain("deny overrides allow");
    expect(markup).toContain("servers");
    expect(markup).toContain("tools");
    expect(markup).toContain("resources");
    expect(markup).toContain("actions");
    expect(markup).toContain("github");
    expect(markup).toContain("delete_issue");
  });
});
