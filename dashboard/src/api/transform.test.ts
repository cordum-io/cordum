import { describe, expect, it, vi, afterEach } from "vitest";
import {
  auditResourceLink,
  computeUrgencyLevel,
  mapApprovalItem,
  mapDLQEntry,
  mapJobDetail,
  mapJobRecord,
  mapPolicyAuditEntry,
  mapPolicyBundleDetail,
  mapPolicyBundleSummary,
  mapPolicyRule,
  mapPolicySnapshot,
  mapPolicySnapshotSummary,
  mapMarketplaceCatalog,
  mapMarketplaceItem,
  mapPackRecord,
  mapSafetyDecision,
  mapHeartbeatToWorker,
  mapWorkflow,
  mapWorkflowRun,
  mapWorkflowRunStep,
  mapWorkflowStep,
  microsToISO,
  normalizeDecisionType,
  normalizeJobStatus,
  mapOutputSafetyRecord,
} from "./transform";

describe("date helpers", () => {
  it("maps valid microseconds and returns empty string for invalid values", () => {
    const micros = 1707000000000000;
    expect(microsToISO(micros)).toBe(new Date(Math.floor(micros / 1000)).toISOString());

    expect(microsToISO(0)).toBe("");
    expect(microsToISO(-1)).toBe("");
    expect(microsToISO(Number.NaN)).toBe("");
    expect(microsToISO("1707000000000000")).toBe("");
    expect(microsToISO(undefined)).toBe("");
    expect(microsToISO(null)).toBe("");
  });

});

describe("pack, marketplace, and heartbeat mapping", () => {
  it("maps pack records using topic capabilities and metadata title", () => {
    const mapped = mapPackRecord({
      id: "pack-1",
      version: "1.0.1",
      status: "installed",
      installed_at: "2026-02-01T00:00:00.000Z",
      installed_by: "ops",
      manifest: {
        metadata: {
          id: "pack-meta-id",
          version: "1.0.0",
          title: "  Search Pack  ",
          description: "Search toolkit",
        },
        topics: [
          { capability: "search.query" },
          { capability: "search.query" },
          { capability: "search.index" },
        ],
      },
      resources: { icon: "search.svg" },
    });

    expect(mapped).toMatchObject({
      id: "pack-1",
      name: "Search Pack",
      version: "1.0.1",
      status: "installed",
      capabilities: ["search.query", "search.index"],
      poolAssignment: undefined,
      installedAt: "2026-02-01T00:00:00.000Z",
      installedBy: "ops",
      description: "Search toolkit",
      resources: { icon: "search.svg" },
    });
  });

  it("falls back to manifest capability/action/tool arrays and title fallback chain", () => {
    const fromCapabilities = mapPackRecord({
      id: "pack-2",
      manifest: {
        metadata: { id: "pack-2-meta", version: "2.0.0" },
        topics: [],
      },
    });
    expect(fromCapabilities.capabilities).toEqual([]);
    expect(fromCapabilities.name).toBe("pack-2-meta");
    expect(fromCapabilities.version).toBe("2.0.0");

    const fromFallbackArrays = mapPackRecord({
      id: "pack-4",
      manifest: {
        metadata: {},
        topics: [],
        actions: [{ name: "action.run" }, { id: "action.stop" }],
        pool_assignment: "batch-pool",
      } as unknown as NonNullable<Parameters<typeof mapPackRecord>[0]["manifest"]>,
    } as unknown as Parameters<typeof mapPackRecord>[0]);
    expect(fromFallbackArrays.capabilities).toEqual(["action.run", "action.stop"]);
    expect(fromFallbackArrays.poolAssignment).toBe("batch-pool");
    expect(fromFallbackArrays.name).toBe("pack-4");
    expect(fromFallbackArrays.status).toBe("unknown");
  });

  it("maps marketplace catalog and item fields from backend shapes", () => {
    expect(
      mapMarketplaceCatalog({
        id: "catalog-1",
        title: "Official",
        url: "https://example.com/catalog",
        enabled: true,
        updated_at: "2026-02-10T00:00:00.000Z",
        error: "none",
      }),
    ).toEqual({
      id: "catalog-1",
      title: "Official",
      url: "https://example.com/catalog",
      enabled: true,
      updatedAt: "2026-02-10T00:00:00.000Z",
      error: "none",
    });

    expect(
      mapMarketplaceItem({
        id: "pack-x",
        version: "1.2.3",
        title: "Pack X",
        description: "A marketplace pack",
        author: "Cordum",
        homepage: "https://example.com/pack-x",
        source: "https://github.com/example/pack-x",
        image: "https://example.com/pack-x.png",
        license: "Apache-2.0",
        url: "https://example.com/install/pack-x",
        sha256: "deadbeef",
        catalog_id: "catalog-1",
        catalog_title: "Official",
        capabilities: ["search"],
        requires: ["db.read"],
        risk_tags: ["pii"],
        installed_version: "1.2.0",
        installed_status: "installed",
        installed_at: "2026-02-11T00:00:00.000Z",
      }),
    ).toEqual({
      id: "pack-x",
      version: "1.2.3",
      title: "Pack X",
      description: "A marketplace pack",
      author: "Cordum",
      homepage: "https://example.com/pack-x",
      source: "https://github.com/example/pack-x",
      image: "https://example.com/pack-x.png",
      license: "Apache-2.0",
      url: "https://example.com/install/pack-x",
      sha256: "deadbeef",
      catalogId: "catalog-1",
      catalogTitle: "Official",
      capabilities: ["search"],
      requires: ["db.read"],
      riskTags: ["pii"],
      installedVersion: "1.2.0",
      installedStatus: "installed",
      installedAt: "2026-02-11T00:00:00.000Z",
    });
  });

  it("maps heartbeat payloads to workers with status and capacity fallback logic", () => {
    expect(mapHeartbeatToWorker({})).toBeNull();
    expect(mapHeartbeatToWorker({ worker_id: "" })).toBeNull();

    const active = mapHeartbeatToWorker({
      worker_id: "worker-1",
      active_jobs: 2,
      max_parallel_jobs: 0,
      labels: { name: "GPU Worker" },
      capabilities: ["gpu.infer"],
      pool: "gpu-pool",
      region: "us-east-1",
      type: "gpu",
      cpu_load: 0.45,
      gpu_utilization: 0.9,
      memory_load: 0.7,
    });
    expect(active).toEqual({
      id: "worker-1",
      name: "GPU Worker",
      pool: "gpu-pool",
      capabilities: ["gpu.infer"],
      status: "active",
      activeJobs: 2,
      capacity: 2,
      region: "us-east-1",
      type: "gpu",
      cpuLoad: 0.45,
      gpuUtilization: 0.9,
      memoryLoad: 0.7,
    });

    const online = mapHeartbeatToWorker({
      worker_id: "worker-2",
      active_jobs: 0,
      max_parallel_jobs: 0,
      labels: { worker_name: "Fallback Name" },
    });
    expect(online).toEqual({
      id: "worker-2",
      name: "Fallback Name",
      pool: "default",
      capabilities: [],
      status: "online",
      activeJobs: 0,
      capacity: 1,
      region: undefined,
      type: undefined,
      cpuLoad: undefined,
      gpuUtilization: undefined,
      memoryLoad: undefined,
    });
  });
});

describe("policy mapping", () => {
  it("maps policy rules and normalizes match criteria keys", () => {
    const mapped = mapPolicyRule({
      id: "rule-1",
      decision: "DECISION_TYPE_REQUIRE_APPROVAL",
      reason: "human approval required",
      match: {
        risk_tags: ["pii"],
        pack_ids: ["pack-1"],
        actor_ids: ["actor-1"],
        actor_types: ["service"],
        secrets_present: true,
        topic: "sys.job.submit",
      },
      priority: 12,
      logic: "all",
      source: { bundle: "default" },
      enabled: true,
    });

    expect(mapped).toEqual({
      id: "rule-1",
      matchCriteria: {
        riskTags: ["pii"],
        packIds: ["pack-1"],
        actorIds: ["actor-1"],
        actorTypes: ["service"],
        secretsPresent: true,
        topic: "sys.job.submit",
      },
      decisionType: "require_approval",
      reason: "human approval required",
      priority: 12,
      logic: "all",
      source: { bundle: "default" },
      enabled: true,
    });
  });

  it("maps policy rules with missing fields to safe defaults", () => {
    expect(mapPolicyRule({})).toEqual({
      id: "",
      matchCriteria: {},
      decisionType: "deny",
      reason: "",
      priority: undefined,
      logic: undefined,
      source: undefined,
      enabled: undefined,
    });
  });

  it("maps policy bundle summaries with YAML rules and parsing fallback", () => {
    const yamlContent = `
rules:
  - id: rule-a
    decision: ALLOW
    reason: safe
    match:
      risk_tags: [pii]
  - id: rule-b
    decision: DECISION_TYPE_THROTTLE
    match:
      pack_ids: [pack-7]
`;
    const mapped = mapPolicyBundleSummary(
      {
        id: "bundle-1",
        enabled: false,
        source: "repo",
        author: "ops",
        message: "policy update",
        created_at: "2026-02-01T00:00:00.000Z",
        updated_at: "2026-02-02T00:00:00.000Z",
        version: "7",
        installed_at: "2026-02-03T00:00:00.000Z",
        sha256: "abc123",
      },
      yamlContent,
    );
    expect(mapped.id).toBe("bundle-1");
    expect(mapped.name).toBe("bundle-1");
    expect(mapped.version).toBe(7);
    expect(mapped.enabled).toBe(false);
    expect(mapped.publishedAt).toBe("2026-02-02T00:00:00.000Z");
    expect(mapped.rules).toHaveLength(2);
    expect(mapped.rules[0]).toMatchObject({
      id: "rule-a",
      decisionType: "allow",
      matchCriteria: { riskTags: ["pii"] },
    });
    expect(mapped.rules[1]).toMatchObject({
      id: "rule-b",
      decisionType: "throttle",
      matchCriteria: { packIds: ["pack-7"] },
    });

    const invalidYaml = mapPolicyBundleSummary(
      { id: "bundle-2", version: "not-a-number" },
      "rules: [",
    );
    expect(invalidYaml.rules).toEqual([]);
    expect(invalidYaml.version).toBeUndefined();
    expect(invalidYaml.enabled).toBe(true);

    const noContent = mapPolicyBundleSummary({ id: "bundle-3" });
    expect(noContent.rules).toEqual([]);
    expect(noContent.enabled).toBe(true);
  });

  it("maps policy bundle detail with content, empty content, and invalid inputs", () => {
    const contentMapped = mapPolicyBundleDetail({
      id: "bundle-detail-1",
      enabled: true,
      author: "ops",
      message: "detail",
      created_at: "2026-02-01T00:00:00.000Z",
      updated_at: "2026-02-02T00:00:00.000Z",
      content: `
rules:
  - id: rule-1
    decision: DENY
`,
    });
    expect(contentMapped).toMatchObject({
      id: "bundle-detail-1",
      name: "bundle-detail-1",
      enabled: true,
      author: "ops",
      message: "detail",
      content: expect.stringContaining("rules"),
    });
    expect(contentMapped.rules).toHaveLength(1);
    expect(contentMapped.rules[0]).toMatchObject({ id: "rule-1", decisionType: "deny" });

    const noContent = mapPolicyBundleDetail({ id: "bundle-detail-2" });
    expect(noContent.rules).toEqual([]);
    expect(noContent.content).toBe("");
    expect(noContent.enabled).toBe(true);

    expect(mapPolicyBundleDetail(null as unknown as never)).toEqual({
      id: "",
      name: "",
      rules: [],
      enabled: true,
      content: "",
    });
    expect(mapPolicyBundleDetail(undefined as unknown as never)).toEqual({
      id: "",
      name: "",
      rules: [],
      enabled: true,
      content: "",
    });
  });

  it("builds audit resource links for all known resource types", () => {
    expect(auditResourceLink("job", "j1")).toBe("/jobs/j1");
    expect(auditResourceLink("workflow", "wf1")).toBe("/workflows/wf1");
    expect(auditResourceLink("run", "r1")).toBe("/workflows");
    expect(auditResourceLink("policy", "p1")).toBe("/policies");
    expect(auditResourceLink("user", "u1")).toBe("/settings");
    expect(auditResourceLink("pack", "pk1")).toBe("/packs");
    expect(auditResourceLink("approval", "a1")).toBe("/approvals");
    expect(auditResourceLink("unknown", "x")).toBe("");
  });

  it("maps policy audit entries with category/severity/actor derivation and parsed snapshots", () => {
    const humanHigh = mapPolicyAuditEntry({
      id: "audit-1",
      action: "edit",
      resource_type: "policy",
      resource_id: "policy-1",
      resource_name: "Default Policy",
      actor_id: "alice",
      role: "admin",
      bundle_ids: ["bundle-1"],
      message: "updated policy rule",
      snapshot_before: "{\"rules\":1}",
      snapshot_after: "{\"rules\":2}",
      created_at: "2026-02-10T01:00:00.000Z",
    });
    expect(humanHigh.category).toBe("human_action");
    expect(humanHigh.severity).toBe("high");
    expect(humanHigh.actorInfo).toEqual({ id: "alice", type: "user", role: "admin" });
    expect(humanHigh.resourceInfo).toEqual({
      type: "policy",
      id: "policy-1",
      name: "Default Policy",
      link: "/policies",
    });
    expect(humanHigh.snapshotBefore).toEqual({ rules: 1 });
    expect(humanHigh.snapshotAfter).toEqual({ rules: 2 });

    const safety = mapPolicyAuditEntry({
      id: "audit-2",
      action: "allow",
      resource_type: "job",
      actor_id: "gateway",
    });
    expect(safety.category).toBe("safety_decision");
    expect(safety.severity).toBe("low");
    expect(safety.actorInfo).toEqual({ id: "gateway", type: "api_key", role: undefined });

    const system = mapPolicyAuditEntry({
      id: "audit-3",
      action: "dispatch",
      resource_type: "job",
      actor_id: "system",
      snapshot_before: "{invalid",
    });
    expect(system.category).toBe("system_event");
    expect(system.severity).toBe("low");
    expect(system.actorInfo).toEqual({ id: "system", type: "system", role: undefined });
    expect(system.snapshotBefore).toBeUndefined();

    const access = mapPolicyAuditEntry({
      id: "audit-4",
      action: "login",
      resource_type: "user",
      actor_id: "user-1",
      role: "user",
    });
    expect(access.category).toBe("access_event");
    expect(access.severity).toBe("low");
  });

  it("maps policy snapshot summary and extracts rules from snapshot bundles", () => {
    expect(
      mapPolicySnapshotSummary({
        id: "snap-1",
        created_at: "2026-02-11T00:00:00.000Z",
        note: "after deploy",
      }),
    ).toEqual({
      id: "snap-1",
      createdAt: "2026-02-11T00:00:00.000Z",
      note: "after deploy",
    });

    const snapshot = mapPolicySnapshot({
      id: "snap-2",
      created_at: "2026-02-12T00:00:00.000Z",
      note: "nightly",
      bundles: {
        bundleA: {
          rules: [{ id: "ra", decision: "ALLOW" }],
        },
        bundleB: {
          rules: [{ id: "rb", decision: "DECISION_TYPE_DENY", match: { actor_ids: ["a1"] } }],
        },
      },
    });
    expect(snapshot.id).toBe("snap-2");
    expect(snapshot.createdAt).toBe("2026-02-12T00:00:00.000Z");
    expect(snapshot.note).toBe("nightly");
    expect(snapshot.rules).toEqual([
      {
        id: "ra",
        matchCriteria: {},
        decisionType: "allow",
        reason: "",
        priority: undefined,
        logic: undefined,
        source: undefined,
        enabled: undefined,
      },
      {
        id: "rb",
        matchCriteria: { actorIds: ["a1"] },
        decisionType: "deny",
        reason: "",
        priority: undefined,
        logic: undefined,
        source: undefined,
        enabled: undefined,
      },
    ]);
  });
});

describe("status and decision normalization", () => {
  it("normalizes all known backend job statuses", () => {
    expect(normalizeJobStatus("PENDING")).toBe("pending");
    expect(normalizeJobStatus("SCHEDULED")).toBe("scheduled");
    expect(normalizeJobStatus("DISPATCHED")).toBe("dispatched");
    expect(normalizeJobStatus("RUNNING")).toBe("running");
    expect(normalizeJobStatus("SUCCEEDED")).toBe("succeeded");
    expect(normalizeJobStatus("FAILED")).toBe("failed");
    expect(normalizeJobStatus("FAILED_RETRYABLE")).toBe("failed");
    expect(normalizeJobStatus("FAILED_FATAL")).toBe("failed");
    expect(normalizeJobStatus("CANCELLED")).toBe("cancelled");
    expect(normalizeJobStatus("APPROVAL_REQUIRED")).toBe("approval_required");
    expect(normalizeJobStatus("DENIED")).toBe("denied");
    expect(normalizeJobStatus("TIMEOUT")).toBe("timeout");
    expect(normalizeJobStatus("UNKNOWN")).toBe("pending");
    expect(normalizeJobStatus(undefined)).toBe("pending");
  });

  it("normalizes all known backend safety decisions", () => {
    expect(normalizeDecisionType("ALLOW")).toBe("allow");
    expect(normalizeDecisionType("ALLOW_WITH_CONSTRAINTS")).toBe("allow");
    expect(normalizeDecisionType("DENY")).toBe("deny");
    expect(normalizeDecisionType("REQUIRE_APPROVAL")).toBe("require_approval");
    expect(normalizeDecisionType("REQUIRE_HUMAN")).toBe("require_approval");
    expect(normalizeDecisionType("THROTTLE")).toBe("throttle");

    expect(normalizeDecisionType("DECISION_TYPE_ALLOW")).toBe("allow");
    expect(normalizeDecisionType("DECISION_TYPE_ALLOW_WITH_CONSTRAINTS")).toBe("allow");
    expect(normalizeDecisionType("DECISION_TYPE_DENY")).toBe("deny");
    expect(normalizeDecisionType("DECISION_TYPE_REQUIRE_HUMAN")).toBe("require_approval");
    expect(normalizeDecisionType("DECISION_TYPE_REQUIRE_APPROVAL")).toBe("require_approval");
    expect(normalizeDecisionType("DECISION_TYPE_THROTTLE")).toBe("throttle");

    expect(normalizeDecisionType("anything-else")).toBe("deny");
    expect(normalizeDecisionType(undefined)).toBe("deny");
  });
});

describe("mapSafetyDecision", () => {
  it("maps full and partial safety decision data", () => {
    expect(mapSafetyDecision("ALLOW", "policy passed", "rule-1")).toEqual({
      type: "allow",
      reason: "policy passed",
      matchedRule: "rule-1",
    });

    expect(mapSafetyDecision(undefined, "reason only", undefined)).toEqual({
      type: "deny",
      reason: "reason only",
      matchedRule: undefined,
    });
  });

  it("returns undefined when no decision data is present", () => {
    expect(mapSafetyDecision(undefined, undefined, undefined)).toBeUndefined();
  });
});

describe("computeUrgencyLevel", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("maps wait time to fresh/aging/critical/breach including boundaries", () => {
    expect(computeUrgencyLevel(0)).toBe("fresh");
    expect(computeUrgencyLevel(119_999)).toBe("fresh");
    expect(computeUrgencyLevel(120_000)).toBe("aging");
    expect(computeUrgencyLevel(899_999)).toBe("aging");
    expect(computeUrgencyLevel(900_000)).toBe("critical");
    expect(computeUrgencyLevel(3_599_999)).toBe("critical");
    expect(computeUrgencyLevel(3_600_000)).toBe("breach");
  });
});

describe("job mapping", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("maps a full backend job record with deduped capabilities and converted timestamps", () => {
    const record = {
      id: "job-1",
      trace_id: "trace-1",
      updated_at: 1_707_000_000_000_000,
      state: "FAILED_RETRYABLE",
      topic: "sys.job.submit",
      tenant: "tenant-a",
      team: "team-x",
      actor_id: "actor-1",
      actor_type: "service",
      capability: "search",
      risk_tags: ["pii"],
      requires: ["read", "search", "read"],
      attempts: 3,
      safety_decision: "ALLOW_WITH_CONSTRAINTS",
      safety_reason: "within policy",
      safety_rule_id: "rule-7",
    };

    const mapped = mapJobRecord(record);
    const expectedIso = new Date(Math.floor(record.updated_at / 1000)).toISOString();
    expect(mapped).toMatchObject({
      id: "job-1",
      type: "",
      topic: "sys.job.submit",
      status: "failed",
      pool: "",
      capabilities: ["search", "read"],
      riskTags: ["pii"],
      metadata: {},
      traceId: "trace-1",
      tenant: "tenant-a",
      team: "team-x",
      actorId: "actor-1",
      actorType: "service",
      capability: "search",
      requires: ["read", "search", "read"],
      attempts: 3,
      safetyDecision: {
        type: "allow",
        reason: "within policy",
        matchedRule: "rule-7",
      },
    });
    expect(mapped.updatedAt).toBe(expectedIso);
    expect(mapped.createdAt).toBe(expectedIso);
  });

  it("maps minimal job records with safe defaults", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-02-13T05:00:00.000Z"));

    const mapped = mapJobRecord({ id: "job-min" });
    expect(mapped).toMatchObject({
      id: "job-min",
      type: "",
      topic: "",
      status: "pending",
      pool: "",
      capabilities: [],
      riskTags: [],
      metadata: {},
      safetyDecision: undefined,
      contextPtr: undefined,
      resultPtr: undefined,
      workflowRunId: undefined,
    });
    expect(mapped.createdAt).toBe("2026-02-13T05:00:00.000Z");
    expect(mapped.updatedAt).toBe("2026-02-13T05:00:00.000Z");
  });

  it("maps job detail fields on top of base job mapping", () => {
    const detail = {
      id: "job-2",
      state: "RUNNING",
      topic: "sys.job.submit",
      context_ptr: "redis://ctx:job-2",
      result_ptr: "redis://res:job-2",
      error_message: "timeout waiting for worker",
      error_status: "DEADLINE_EXCEEDED",
      error_code: "E_TIMEOUT",
      last_state: "DISPATCHED",
      run_id: "run-9",
      workflow_id: "wf-3",
      safety_decision: "REQUIRE_APPROVAL",
    };

    const mapped = mapJobDetail(detail);
    expect(mapped.status).toBe("running");
    expect(mapped.contextPtr).toBe("redis://ctx:job-2");
    expect(mapped.resultPtr).toBe("redis://res:job-2");
    expect(mapped.errorMessage).toBe("timeout waiting for worker");
    expect(mapped.errorStatus).toBe("DEADLINE_EXCEEDED");
    expect(mapped.errorCode).toBe("E_TIMEOUT");
    expect(mapped.lastState).toBe("DISPATCHED");
    expect(mapped.workflowRunId).toBe("run-9");
    expect(mapped.workflowId).toBe("wf-3");
    expect(mapped.safetyDecision?.type).toBe("require_approval");
  });
});

describe("workflow mapping", () => {
  it("normalizes workflow step node types and fallback IDs", () => {
    const minimal = mapWorkflowStep({}, "fallback-step");
    expect(minimal).toMatchObject({
      id: "fallback-step",
      name: "fallback-step",
      type: "agent-task",
      config: {},
    });

    expect(mapWorkflowStep({ type: "job" }, "job-step").type).toBe("agent-task");
    expect(
      mapWorkflowStep({ type: "job", meta: { pack_id: "pack-1" } }, "pack-step").type,
    ).toBe("pack-action");
    expect(
      mapWorkflowStep({ type: "job", meta: { capability: "shell.exec" } }, "tool-step").type,
    ).toBe("tool-call");
    expect(
      mapWorkflowStep(
        { type: "job", for_each: "$.items[*]", meta: { capability: "shell.exec", prompt: "x" } },
        "fanout-step",
      ).type,
    ).toBe("fan-out");

    expect(mapWorkflowStep({ type: "approval" }, "approval-step").type).toBe("approval");
    expect(mapWorkflowStep({ type: "delay" }, "delay-step").type).toBe("delay");
    expect(mapWorkflowStep({ type: "condition" }, "condition-step").type).toBe("condition");
    expect(mapWorkflowStep({ type: "notify" }, "notify-step").type).toBe("notify");

    const unknown = mapWorkflowStep({ type: "custom_node" }, "unknown-step");
    expect(unknown.type).toBe("agent-task");
    expect(unknown.config).toMatchObject({ backendType: "custom_node" });
  });

  it("extracts workflow step config fields from backend step payloads", () => {
    const step = mapWorkflowStep(
      {
        id: "step-1",
        name: "Step 1",
        type: "job",
        worker_id: "worker-a",
        topic: "sys.job.submit",
        depends_on: ["prev"],
        condition: "input.ok",
        for_each: "$.items[*]",
        max_parallel: 4,
        timeout_sec: 30,
        delay_sec: 10,
        input: {
          message: "hello",
          component: "slack",
          prompt: "Summarize",
          budget: {
            input_tokens: 11,
            output_tokens: 22,
            total_tokens: 33,
          },
        },
        input_schema: { type: "object" },
        input_schema_id: "schema-in",
        output_schema: { type: "string" },
        output_schema_id: "schema-out",
        output_path: "$.result",
        route_labels: { region: "us-east" },
        retry: {
          max_retries: 3,
          initial_backoff_sec: 5,
          multiplier: 2,
        },
        meta: {
          capability: "search",
          requires: ["db.read", "cache.read"],
          risk_tags: ["pii"],
          labels: { team: "ops" },
          pack_id: "pack-1",
          actor_id: "actor-1",
          actor_type: "service",
          adapter_id: "adapter-1",
          memory_id: "mem-1",
          context_mode: "windowed",
          allow_summarization: true,
          allow_retrieval: false,
          deadline_ms: 1_700,
          priority: "high",
        },
      },
      "fallback-step",
    );

    expect(step.type).toBe("pack-action");
    expect(step.retry).toEqual({
      max_retries: 3,
      backoff_sec: 5,
      backoff_multiplier: 2,
    });
    expect(step.config).toMatchObject({
      topic: "sys.job.submit",
      workerId: "worker-a",
      timeout: "30s",
      retryMax: 3,
      expression: "input.ok",
      forEach: "$.items[*]",
      parallelism: 4,
      duration: "10s",
      input: {
        message: "hello",
        component: "slack",
        prompt: "Summarize",
        budget: {
          input_tokens: 11,
          output_tokens: 22,
          total_tokens: 33,
        },
      },
      messageTemplate: "hello",
      channel: "slack",
      prompt: "Summarize",
      maxInputTokens: 11,
      maxOutputTokens: 22,
      maxTotalTokens: 33,
      capabilities: ["search", "db.read", "cache.read"],
      riskTags: ["pii"],
      labels: { team: "ops" },
      packId: "pack-1",
      actorId: "actor-1",
      actorType: "service",
      adapterId: "adapter-1",
      memoryId: "mem-1",
      contextMode: "windowed",
      allowSummarization: true,
      allowRetrieval: false,
      deadlineMs: 1700,
      priority: "high",
      routeLabels: { region: "us-east" },
      inputSchema: { type: "object" },
      inputSchemaId: "schema-in",
      outputSchema: { type: "string" },
      outputSchemaId: "schema-out",
      outputPath: "$.result",
    });
  });

  it("maps workflow definitions with metadata and step maps", () => {
    const mapped = mapWorkflow({
      id: "wf-1",
      org_id: "org-1",
      team_id: "team-1",
      name: "Ingest Workflow",
      description: "Ingest and summarize",
      version: "2",
      timeout_sec: 120,
      config: { strategy: "safe" },
      input_schema: { type: "object" },
      parameters: [{ name: "query" }],
      created_at: "2026-01-01T00:00:00.000Z",
      updated_at: "2026-01-02T00:00:00.000Z",
      steps: {
        s1: { type: "job", topic: "sys.job.submit" },
        s2: { type: "approval" },
      },
    });

    expect(mapped.id).toBe("wf-1");
    expect(mapped.name).toBe("Ingest Workflow");
    expect(mapped.timeout_sec).toBe(120);
    expect(mapped.timeout).toBe(120);
    expect(mapped.steps).toHaveLength(2);
    expect(mapped.steps[0].id).toBe("s1");
    expect(mapped.steps[1].type).toBe("approval");
    expect(mapped.metadata).toEqual({
      orgId: "org-1",
      teamId: "team-1",
      description: "Ingest and summarize",
      version: "2",
      config: { strategy: "safe" },
      inputSchema: { type: "object" },
      parameters: [{ name: "query" }],
    });
  });

  it("maps workflows without steps to an empty array", () => {
    const mapped = mapWorkflow({ id: "wf-empty" });
    expect(mapped.name).toBe("wf-empty");
    expect(mapped.steps).toEqual([]);
    expect(mapped.timeout).toBe(0);
  });

  it("maps workflow run steps and run-level fields", () => {
    const runStep = mapWorkflowRunStep(
      {
        step_id: "step-99",
        status: "completed",
        output: { ok: true },
        error: { code: "E_FAIL" },
        started_at: "2026-02-10T10:00:00.000Z",
        completed_at: "2026-02-10T10:00:10.000Z",
      },
      "fallback-id",
    );
    expect(runStep).toEqual({
      id: "step-99",
      name: "step-99",
      type: "step",
      status: "completed",
      output: { ok: true },
      error: "{\"code\":\"E_FAIL\"}",
      startedAt: "2026-02-10T10:00:00.000Z",
      completedAt: "2026-02-10T10:00:10.000Z",
    });

    const run = mapWorkflowRun({
      id: "run-1",
      workflow_id: "wf-1",
      org_id: "org-1",
      team_id: "team-1",
      status: "running",
      started_at: "2026-02-10T10:00:00.000Z",
      completed_at: null,
      created_at: "2026-02-10T09:59:00.000Z",
      updated_at: "2026-02-10T10:00:05.000Z",
      input: { q: "hello" },
      output: { done: false },
      error: { reason: "none" },
      rerun_of: "run-0",
      rerun_step: "s2",
      dry_run: true,
      steps: {
        s1: { status: "completed", output: { ok: true } },
      },
    });
    expect(run).toMatchObject({
      id: "run-1",
      workflowId: "wf-1",
      status: "running",
      startedAt: "2026-02-10T10:00:00.000Z",
      completedAt: undefined,
      createdAt: "2026-02-10T09:59:00.000Z",
      updatedAt: "2026-02-10T10:00:05.000Z",
      orgId: "org-1",
      teamId: "team-1",
      input: { q: "hello" },
      output: { done: false },
      error: { reason: "none" },
      rerunOf: "run-0",
      rerunStep: "s2",
      dryRun: true,
    });
    expect(run.steps).toHaveLength(1);
    expect(run.steps[0].id).toBe("s1");
    expect(run.steps[0].name).toBe("s1");
  });

  it("maps workflow runs without optional fields safely", () => {
    const run = mapWorkflowRun({ id: "run-empty" });
    expect(run.workflowId).toBe("");
    expect(run.status).toBe("pending");
    expect(run.steps).toEqual([]);
    expect(run.startedAt).toBe("");
    expect(run.completedAt).toBeUndefined();
    expect(run.rerunOf).toBeUndefined();
    expect(run.rerunStep).toBeUndefined();
  });
});

describe("approval and DLQ mapping", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns null approval when job data is missing", () => {
    expect(mapApprovalItem({})).toBeNull();
  });

  it("maps full approval records with workflow context and enriched summary", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-02-13T05:00:00.000Z"));

    const approval = mapApprovalItem({
      job: {
        id: "job-approval-1",
        state: "RUNNING",
        topic: "sys.job.submit",
        tenant: "tenant-1",
        actor_id: "actor-7",
        capability: "search",
        requires: ["db.read"],
        risk_tags: ["pii"],
        updated_at: Date.parse("2026-02-13T04:50:00.000Z") * 1000,
        safety_decision: "REQUIRE_APPROVAL",
      },
      decision: "approve",
      policy_rule_id: "rule-9",
      policy_reason: "manual review needed",
      approval_ref: "approval-1",
      job_hash: "hash-1",
      policy_snapshot: "snap-1",
      context_ptr: "redis://ctx:job-approval-1",
      resolved_at: Date.parse("2026-02-13T04:55:00.000Z") * 1000,
      resolved_by: "admin-1",
      resolved_comment: "approved after review",
      constraints: { redact: true },
      workflow_id: "wf-10",
      workflow_run_id: "run-10",
      step_index: 2,
      step_name: "SafetyGate",
      total_steps: 5,
    });

    expect(approval).not.toBeNull();
    expect(approval).toMatchObject({
      id: "approval-1",
      jobId: "job-approval-1",
      status: "approved",
      actor: "admin-1",
      actorId: "actor-7",
      reason: "manual review needed",
      comment: "approved after review",
      policyRule: "rule-9",
      topic: "sys.job.submit",
      safetyDecision: { type: "require_approval", reason: "", matchedRule: undefined },
      riskTags: ["pii"],
      capabilities: ["search", "db.read"],
      workflowContext: {
        workflowId: "wf-10",
        runId: "run-10",
        stepIndex: 2,
        stepName: "SafetyGate",
        totalSteps: 5,
      },
      urgencyLevel: "aging",
      waitMs: 600000,
      policySnapshot: "snap-1",
      jobHash: "hash-1",
      approvalRef: "approval-1",
      tenant: "tenant-1",
      contextPtr: "redis://ctx:job-approval-1",
      constraints: { redact: true },
    });
    expect(approval?.requestedAt).toBe("2026-02-13T04:50:00.000Z");
    expect(approval?.resolvedAt).toBe("2026-02-13T04:55:00.000Z");
    expect(approval?.humanSummary).toContain('Job on "sys.job.submit"');
    expect(approval?.humanSummary).toContain("requires search, db.read");
    expect(approval?.humanSummary).toContain("manual review needed");
    expect(approval?.jobContext).toEqual({
      topic: "sys.job.submit",
      tenant: "tenant-1",
      capabilities: ["search", "db.read"],
      riskTags: ["pii"],
    });
  });

  it("derives rejected/resolved/pending approval statuses and workflow context absence", () => {
    const rejected = mapApprovalItem({
      job: { id: "job-reject", state: "RUNNING" },
      decision: "reject",
    });
    expect(rejected?.status).toBe("rejected");
    expect(rejected?.workflowContext).toBeUndefined();

    const resolved = mapApprovalItem({
      job: { id: "job-resolved", state: "succeeded" },
    });
    expect(resolved?.status).toBe("resolved");

    const pending = mapApprovalItem({
      job: { id: "job-pending", state: "RUNNING" },
    });
    expect(pending?.status).toBe("pending");
  });

  it("maps DLQ entries with full and minimal payloads", () => {
    expect(
      mapDLQEntry({
        job_id: "job-dlq-1",
        topic: "sys.job.submit",
        status: "failed",
        reason: "worker crashed",
        reason_code: "E_WORKER",
        last_state: "RUNNING",
        attempts: 2,
        created_at: "2026-02-12T10:00:00.000Z",
      }),
    ).toEqual({
      id: "job-dlq-1",
      jobId: "job-dlq-1",
      error: "worker crashed",
      retryCount: 2,
      maxRetries: 0,
      originalTopic: "sys.job.submit",
      failedAt: "2026-02-12T10:00:00.000Z",
      status: "failed",
      reasonCode: "E_WORKER",
      lastState: "RUNNING",
      reason: "worker crashed",
      attempts: 2,
      createdAt: "2026-02-12T10:00:00.000Z",
    });

    expect(mapDLQEntry({ job_id: "job-dlq-min" })).toEqual({
      id: "job-dlq-min",
      jobId: "job-dlq-min",
      error: "",
      retryCount: 0,
      maxRetries: 0,
      originalTopic: "",
      failedAt: "",
      status: undefined,
      reasonCode: undefined,
      lastState: undefined,
      reason: undefined,
      attempts: undefined,
      createdAt: undefined,
    });
  });
});

describe("mapOutputSafetyRecord", () => {
  it("returns undefined for null/undefined input", () => {
    expect(mapOutputSafetyRecord(undefined)).toBeUndefined();
    expect(mapOutputSafetyRecord(null as never)).toBeUndefined();
  });

  it("returns undefined for non-object input", () => {
    expect(mapOutputSafetyRecord("string" as never)).toBeUndefined();
    expect(mapOutputSafetyRecord(42 as never)).toBeUndefined();
  });

  it("defaults findings to empty array when not provided", () => {
    const result = mapOutputSafetyRecord({ decision: "QUARANTINE" });
    expect(result).toBeDefined();
    expect(result?.findings).toEqual([]);
    expect(result?.decision).toBe("QUARANTINE");
  });

  it("maps full output safety record with findings", () => {
    const result = mapOutputSafetyRecord({
      decision: "QUARANTINE",
      reason: "PII detected",
      rule_id: "rule-1",
      findings: [
        { type: "pii", severity: "high", detail: "SSN found" },
      ],
      phase: "async",
      policy_snapshot: "snap-1",
      redacted_ptr: "ptr-redacted",
      original_ptr: "ptr-original",
    });
    expect(result).toEqual({
      decision: "QUARANTINE",
      reason: "PII detected",
      rule_id: "rule-1",
      findings: [
        { type: "pii", severity: "high", detail: "SSN found", scanner: undefined, confidence: undefined, matched_pattern: undefined, offset: undefined, length: undefined },
      ],
      phase: "async",
      policy_snapshot: "snap-1",
      redacted_ptr: "ptr-redacted",
      original_ptr: "ptr-original",
    });
  });

  it("handles partial data with missing optional fields", () => {
    const result = mapOutputSafetyRecord({});
    expect(result).toBeDefined();
    expect(result?.decision).toBe("ALLOW");
    expect(result?.reason).toBeUndefined();
    expect(result?.rule_id).toBeUndefined();
    expect(result?.findings).toEqual([]);
  });
});
