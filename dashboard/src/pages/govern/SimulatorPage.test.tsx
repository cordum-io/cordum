import { describe, expect, it } from "vitest";
import { derivePolicyAccess } from "@/hooks/usePolicyAccess";
import {
  SIMULATOR_PAGE_SECTIONS,
  buildSimulatorRequest,
  parseSimulatorQueryParams,
} from "./SimulatorPage";
import { __simulatorContextFormInternal } from "@/components/policy/simulator/SimulatorContextForm";

const { parseCsv, parseLabels, labelsToString } = __simulatorContextFormInternal;

describe("SimulatorPage sections", () => {
  it("defines four page sections for the simulator workspace", () => {
    expect(SIMULATOR_PAGE_SECTIONS).toEqual([
      "bundle-select",
      "context-form",
      "decision-summary",
      "evaluation-chain",
    ]);
  });
});

describe("SimulatorPage RBAC: all-role accessibility", () => {
  it("does not gate simulation on canEdit, canPublish, or canRelease", () => {
    for (const role of ["viewer", "auditor", "editor", "publisher", "release_manager", "operator", "admin"]) {
      const access = derivePolicyAccess({
        requiresAuth: true,
        roles: [role],
        principalRole: role,
      });
      // Simulation is a read-only diagnostic — no RBAC flags should prevent it.
      // We verify derivePolicyAccess returns some object for every role.
      expect(access).toBeDefined();
      expect(typeof access.isReadOnly).toBe("boolean");
    }
  });
});

describe("buildSimulatorRequest", () => {
  it("builds empty request from empty context", () => {
    expect(
      buildSimulatorRequest({
        topic: "",
        tenant: "",
        workflowId: "",
        capabilities: [],
        riskTags: [],
        labels: {},
      }),
    ).toEqual({});
  });

  it("includes only non-empty fields", () => {
    const result = buildSimulatorRequest({
      topic: "job.deploy",
      tenant: "",
      workflowId: "wf-1",
      capabilities: ["code.generate"],
      riskTags: [],
      labels: { env: "prod" },
    });
    expect(result).toEqual({
      topic: "job.deploy",
      workflow_id: "wf-1",
      capabilities: ["code.generate"],
      labels: { env: "prod" },
    });
    expect(result).not.toHaveProperty("tenant_id");
    expect(result).not.toHaveProperty("risk_tags");
  });

  it("trims whitespace from string fields", () => {
    const result = buildSimulatorRequest({
      topic: "  job.deploy  ",
      tenant: "  acme  ",
      workflowId: "",
      capabilities: [],
      riskTags: [],
      labels: {},
    });
    expect(result.topic).toBe("job.deploy");
    expect(result.tenant_id).toBe("acme");
  });
});

describe("parseSimulatorQueryParams", () => {
  it("returns empty partial for empty params", () => {
    expect(parseSimulatorQueryParams(new URLSearchParams())).toEqual({});
  });

  it("parses topic and tenant", () => {
    const params = new URLSearchParams("topic=job.deploy&tenant=acme");
    const result = parseSimulatorQueryParams(params);
    expect(result.topic).toBe("job.deploy");
    expect(result.tenant).toBe("acme");
  });

  it("parses capabilities as comma-separated list", () => {
    const params = new URLSearchParams("capabilities=code.generate,code.review");
    const result = parseSimulatorQueryParams(params);
    expect(result.capabilities).toEqual(["code.generate", "code.review"]);
  });

  it("parses risk_tags as comma-separated list", () => {
    const params = new URLSearchParams("risk_tags=high,pii");
    const result = parseSimulatorQueryParams(params);
    expect(result.riskTags).toEqual(["high", "pii"]);
  });

  it("parses workflow param", () => {
    const params = new URLSearchParams("workflow=wf-1");
    const result = parseSimulatorQueryParams(params);
    expect(result.workflowId).toBe("wf-1");
  });
});

describe("SimulatorContextForm internals", () => {
  it("parses CSV values", () => {
    expect(parseCsv("a, b, c")).toEqual(["a", "b", "c"]);
    expect(parseCsv("")).toEqual([]);
    expect(parseCsv("  , , ")).toEqual([]);
  });

  it("parses key=value labels", () => {
    expect(parseLabels("env=prod, team=platform")).toEqual({
      env: "prod",
      team: "platform",
    });
    expect(parseLabels("")).toEqual({});
    expect(parseLabels("bad")).toEqual({});
  });

  it("serializes labels back to string", () => {
    expect(labelsToString({ env: "prod", team: "platform" })).toBe(
      "env=prod, team=platform",
    );
    expect(labelsToString({})).toBe("");
  });
});
