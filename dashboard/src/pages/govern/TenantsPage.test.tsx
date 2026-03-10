import { describe, expect, it } from "vitest";
import { derivePolicyAccess } from "@/hooks/usePolicyAccess";
import { parseTenantSummaries, TENANTS_PAGE_SECTIONS } from "./TenantsPage";

describe("TenantsPage extraction contract", () => {
  it("keeps page sections focused on tenant bundle/list responsibilities", () => {
    expect(TENANTS_PAGE_SECTIONS).toEqual([
      "tenant-bundle-select",
      "tenant-summary-cards",
      "tenant-list",
    ]);
    expect(TENANTS_PAGE_SECTIONS).not.toContain("simulator");
    expect(TENANTS_PAGE_SECTIONS).not.toContain("output-rules");
  });

  it("derives tenant summary stats from bundle tenant map", () => {
    const summaries = parseTenantSummaries({
      tenants: {
        "acme-corp": {
          label: "Acme Corp",
          allow_topics: ["job.default", "job.customer.*"],
          max_concurrent_jobs: 40,
          mcp: {
            allow_servers: ["github", "slack"],
            deny_servers: ["internal-admin"],
          },
        },
      },
    });

    expect(summaries).toHaveLength(1);
    expect(summaries[0]).toMatchObject({
      id: "acme-corp",
      label: "Acme Corp",
      allowTopicsCount: 2,
      mcpServersCount: 3,
      maxConcurrentJobs: 40,
    });
  });

  it("enforces tenant write affordances by role", () => {
    const viewerAccess = derivePolicyAccess({
      requiresAuth: true,
      roles: ["viewer"],
      principalRole: "viewer",
    });
    expect(viewerAccess.canManageTenants).toBe(false);

    const operatorAccess = derivePolicyAccess({
      requiresAuth: true,
      roles: ["operator"],
      principalRole: "operator",
    });
    expect(operatorAccess.canManageTenants).toBe(true);
  });
});
