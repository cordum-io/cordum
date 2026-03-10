import { describe, expect, it } from "vitest";
import { buildSimulatorUrl } from "./simulatorQuery";

describe("buildSimulatorUrl", () => {
  it("returns bare path when no params provided", () => {
    expect(buildSimulatorUrl({})).toBe("/govern/simulator");
  });

  it("includes bundle param when bundleId is set", () => {
    const url = buildSimulatorUrl({ bundleId: "default" });
    expect(url).toBe("/govern/simulator?bundle=default");
  });

  it("includes topic and tenant params", () => {
    const url = buildSimulatorUrl({ topic: "job.deploy", tenant: "acme" });
    expect(url).toContain("topic=job.deploy");
    expect(url).toContain("tenant=acme");
  });

  it("encodes capabilities as comma-separated list", () => {
    const url = buildSimulatorUrl({ capabilities: ["code.generate", "code.review"] });
    expect(url).toContain("capabilities=code.generate%2Ccode.review");
  });

  it("encodes risk_tags as comma-separated list", () => {
    const url = buildSimulatorUrl({ riskTags: ["high", "pii"] });
    expect(url).toContain("risk_tags=high%2Cpii");
  });

  it("ignores empty/whitespace-only params", () => {
    const url = buildSimulatorUrl({ bundleId: "  ", topic: "", capabilities: [] });
    expect(url).toBe("/govern/simulator");
  });

  it("includes workflow param", () => {
    const url = buildSimulatorUrl({ workflowId: "wf-prod-deploy" });
    expect(url).toContain("workflow=wf-prod-deploy");
  });

  it("combines all params", () => {
    const url = buildSimulatorUrl({
      bundleId: "secops/prod",
      topic: "job.deploy",
      tenant: "acme",
      workflowId: "wf-1",
      capabilities: ["code.generate"],
      riskTags: ["high"],
    });
    expect(url).toContain("bundle=secops%2Fprod");
    expect(url).toContain("topic=job.deploy");
    expect(url).toContain("tenant=acme");
    expect(url).toContain("workflow=wf-1");
    expect(url).toContain("capabilities=code.generate");
    expect(url).toContain("risk_tags=high");
  });
});
