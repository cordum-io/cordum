import { describe, expect, it } from "vitest";
import { hashKey } from "@tanstack/react-query";
import { API_PATHS } from "./constants";
import { queryKeys } from "./queryKeys";

describe("queryKeys factory", () => {
  // ── Structure tests ─────────────────────────────────────────────
  it("jobs.list produces [jobs, filters]", () => {
    const filters = { state: ["running" as const], topic: "job.default" };
    expect(queryKeys.jobs.list(filters)).toEqual(["jobs", filters]);
  });

  it("jobs.detail produces [job, id]", () => {
    expect(queryKeys.jobs.detail("abc-123")).toEqual(["job", "abc-123"]);
  });

  it("jobs.quarantined produces [jobs, quarantined, filters]", () => {
    const filters = { limit: 10 };
    expect(queryKeys.jobs.quarantined(filters)).toEqual(["jobs", "quarantined", filters]);
  });

  it("approvals.list defaults status to all", () => {
    expect(queryKeys.approvals.list()).toEqual(["approvals", "all"]);
    expect(queryKeys.approvals.list("pending")).toEqual(["approvals", "pending"]);
  });

  it("approvals.all is a prefix array", () => {
    expect(queryKeys.approvals.all).toEqual(["approvals"]);
  });

  it("dlq.list produces [dlq, filters]", () => {
    expect(queryKeys.dlq.list({ limit: 20 })).toEqual(["dlq", { limit: 20 }]);
  });

  it("audit keys have expected shape", () => {
    expect(queryKeys.audit.all).toEqual(["audit"]);
    expect(queryKeys.audit.event("e1")).toEqual(["audit", "event", "e1"]);
    expect(queryKeys.audit.correlation("r1")).toEqual(["audit", "correlation", "r1"]);
  });

  it("workflows keys have expected shape", () => {
    expect(queryKeys.workflows.all).toEqual(["workflows"]);
    expect(queryKeys.workflows.list()).toEqual(["workflows", {}]);
    expect(queryKeys.workflows.detail("wf-1")).toEqual(["workflow", "wf-1"]);
  });

  it("workflowRuns keys have expected shape", () => {
    expect(queryKeys.workflowRuns.all).toEqual(["workflow-runs"]);
    expect(queryKeys.workflowRuns.active()).toEqual(["workflow-runs", "active"]);
    expect(queryKeys.workflowRuns.detail("r1")).toEqual(["workflow-run", "r1"]);
    expect(queryKeys.workflowRuns.timeline("r1", 50)).toEqual([
      "workflow-run", "r1", "timeline", 50,
    ]);
    expect(queryKeys.workflowRuns.timeline("r1")).toEqual([
      "workflow-run", "r1", "timeline", "default",
    ]);
  });

  it("policies keys have expected shape", () => {
    expect(queryKeys.policies.bundles()).toEqual(["policy-bundles"]);
    expect(queryKeys.policies.bundle("secops/default")).toEqual(["policy-bundle", "secops/default"]);
    expect(queryKeys.policies.rules()).toEqual(["policy-rules"]);
    expect(queryKeys.policies.velocityRules()).toEqual(["policy-velocity-rules"]);
    expect(queryKeys.policies.velocityRuleStats()).toEqual(["policy-velocity-rules", "stats"]);
    expect(queryKeys.policies.config()).toEqual(["policy-config"]);
  });

  it("outputPolicy keys have expected shape", () => {
    expect(queryKeys.outputPolicy.config()).toEqual(["output-policy-config"]);
    expect(queryKeys.outputPolicy.stats()).toEqual(["output-policy", "stats"]);
  });

  // ── hashKey stability tests ─────────────────────────────────────
  it("identical filter objects produce the same hash", () => {
    const a = queryKeys.jobs.list({ state: ["running" as const], topic: "job.default" });
    const b = queryKeys.jobs.list({ state: ["running" as const], topic: "job.default" });
    expect(hashKey(a)).toBe(hashKey(b));
  });

  it("different filter objects produce different hashes", () => {
    const a = queryKeys.jobs.list({ topic: "job.default" });
    const b = queryKeys.jobs.list({ topic: "job.other" });
    expect(hashKey(a)).not.toBe(hashKey(b));
  });

  it("filter key order does not affect hash", () => {
    const a = queryKeys.jobs.list({ topic: "t", pool: "p" } as never);
    const b = queryKeys.jobs.list({ pool: "p", topic: "t" } as never);
    expect(hashKey(a)).toBe(hashKey(b));
  });

  // ── Invalidation prefix tests ──────────────────────────────────
  it("jobs.all is a prefix of jobs.list", () => {
    // The prefix array ["jobs"] should be a subset of ["jobs", {topic: "t"}]
    expect(queryKeys.jobs.list({ topic: "t" })[0]).toBe(queryKeys.jobs.all[0]);
  });

  it("approvals.all is a prefix of approvals.list", () => {
    const list = queryKeys.approvals.list("pending");
    expect(list[0]).toBe(queryKeys.approvals.all[0]);
  });

  it("edge API paths match the Edge route namespace", () => {
    expect(API_PATHS.edge).toEqual({
      sessions: "/edge/sessions",
      executions: "/edge/executions",
      approvals: "/edge/approvals",
      evaluate: "/edge/evaluate",
      events: "/edge/events",
      eventsBatch: "/edge/events/batch",
    });
  });

  it("edge query keys are prefix-friendly and avoid broad filter objects", () => {
    expect(queryKeys.edge.all).toEqual(["edge"]);
    expect(queryKeys.edge.sessions.all).toEqual(["edge", "sessions"]);
    expect(queryKeys.edge.sessions.lists()).toEqual(["edge", "sessions", "list"]);
    expect(queryKeys.edge.sessions.list({
      status: "running",
      principalId: "user-1",
      agentProduct: "claude-code",
      cursor: "cur-1",
      limit: 25,
    })).toEqual([
      "edge",
      "sessions",
      "list",
      ["running", "user-1", "claude-code", "cur-1", 25],
    ]);
    expect(queryKeys.edge.sessions.detail("edge_sess_1")).toEqual([
      "edge",
      "sessions",
      "detail",
      "edge_sess_1",
    ]);
    expect(queryKeys.edge.sessions.events("edge_sess_1", {
      kind: "hook.pre_tool_use",
      decision: "DENY",
      cursor: "event-cur",
      limit: 50,
    })).toEqual([
      "edge",
      "sessions",
      "events",
      "edge_sess_1",
      ["hook.pre_tool_use", "DENY", "since:all", "until:all", "event-cur", 50],
    ]);
    expect(queryKeys.edge.sessions.eventLists("edge_sess_1")).toEqual([
      "edge",
      "sessions",
      "events",
      "edge_sess_1",
    ]);
    expect(queryKeys.edge.sessions.export("edge_sess_1")).toEqual([
      "edge",
      "sessions",
      "export",
      "edge_sess_1",
    ]);
    expect(queryKeys.edge.executions.lists()).toEqual(["edge", "executions", "list"]);
    expect(queryKeys.edge.executions.list({
      sessionId: "edge_sess_1",
      jobId: "job-1",
      traceId: "trace-1",
      workflowRunId: "run-1",
      cursor: "cur-1",
      limit: 10,
    })).toEqual([
      "edge",
      "executions",
      "list",
      ["edge_sess_1", "job-1", "trace-1", "run-1", "cur-1", 10],
    ]);
    expect(queryKeys.edge.executions.detail("exec-1")).toEqual([
      "edge",
      "executions",
      "detail",
      "exec-1",
    ]);
    expect(queryKeys.edge.executions.events("exec-1")).toEqual([
      "edge",
      "executions",
      "events",
      "exec-1",
      ["all", "all", "since:all", "until:all", "first", "default"],
    ]);
    expect(queryKeys.edge.approvals.lists()).toEqual(["edge", "approvals", "list"]);
    expect(queryKeys.edge.approvals.list({
      status: "pending",
      sessionId: "edge_sess_1",
      executionId: "exec-1",
      actionHash: "sha256:action",
    })).toEqual([
      "edge",
      "approvals",
      "list",
      ["pending", "edge_sess_1", "exec-1", "sha256:action", "first", "default"],
    ]);
    expect(queryKeys.edge.approvals.detail("edge_appr_1")).toEqual([
      "edge",
      "approvals",
      "detail",
      "edge_appr_1",
    ]);
  });
});
