import { describe, expect, it } from "vitest";
import { computeWorkflowOutput, renderTemplate, topoSortTaskNodes, type TemplateContext } from "./runWorkflow";
import type { Workflow } from "../types";

describe("renderTemplate", () => {
  it("replaces input variables", () => {
    const ctx: TemplateContext = {
      inputs: { prompt: "CODE", filePath: "a.go", instruction: "do it" },
      nodeResults: {},
      prevResult: null,
    };
    expect(renderTemplate("x ${input.prompt} y", ctx)).toBe("x CODE y");
    expect(renderTemplate("${input.filePath}", ctx)).toBe("a.go");
    expect(renderTemplate("${input.instruction}", ctx)).toBe("do it");
  });

  it("replaces prev.result and node result path", () => {
    const ctx: TemplateContext = {
      inputs: { prompt: "" },
      nodeResults: { n1: { result: { a: { b: 2 } } } },
      prevResult: { ok: true },
    };
    expect(renderTemplate("${node.n1.result.a.b}", ctx)).toBe("2");
    expect(renderTemplate("prev=${prev.result}", ctx)).toContain("\"ok\": true");
    expect(renderTemplate("${prev.result.ok}", ctx)).toBe("true");
  });

  it("supports prev/node pointer helpers", () => {
    const ctx: TemplateContext = {
      inputs: { prompt: "" },
      nodeResults: {
        n1: { result: "ok", resultPtr: "redis://res:n1", context_ptr: "redis://ctx:n1" },
      },
      prevResult: "prev",
      prevResultPtr: "redis://res:prev",
      prevContextPtr: "redis://ctx:prev",
      prevNodeId: "n1",
    };
    expect(renderTemplate("${prev.result_ptr}", ctx)).toBe("redis://res:prev");
    expect(renderTemplate("${prev.contextPtr}", ctx)).toBe("redis://ctx:prev");
    expect(renderTemplate("${prev.node_id}", ctx)).toBe("n1");
    expect(renderTemplate("${node.n1.result_ptr}", ctx)).toBe("redis://res:n1");
    expect(renderTemplate("${node.n1.context_ptr}", ctx)).toBe("redis://ctx:n1");
  });
});

describe("topoSortTaskNodes", () => {
  it("orders tasks by dependencies", () => {
    const wf: Workflow = {
      id: "wf",
      name: "wf",
      updatedAt: 0,
      nodes: [
        { id: "a", type: "task", position: { x: 0, y: 0 }, data: { kind: "task", name: "A", topic: "job.echo", promptTemplate: "", timeoutMs: 0, retries: 0 } },
        { id: "b", type: "task", position: { x: 10, y: 0 }, data: { kind: "task", name: "B", topic: "job.echo", promptTemplate: "", timeoutMs: 0, retries: 0 } },
        { id: "c", type: "task", position: { x: 20, y: 0 }, data: { kind: "task", name: "C", topic: "job.echo", promptTemplate: "", timeoutMs: 0, retries: 0 } },
      ],
      edges: [
        { id: "e1", source: "a", target: "b" },
        { id: "e2", source: "b", target: "c" },
      ],
    };
    expect(topoSortTaskNodes(wf).map((n) => n.id)).toEqual(["a", "b", "c"]);
  });

  it("treats memory nodes as pass-through for dependencies", () => {
    const wf: Workflow = {
      id: "wf",
      name: "wf",
      updatedAt: 0,
      nodes: [
        { id: "a", type: "task", position: { x: 300, y: 0 }, data: { kind: "task", name: "A", topic: "job.echo", promptTemplate: "", timeoutMs: 0, retries: 0 } },
        { id: "m", type: "memory", position: { x: 150, y: 0 }, data: { kind: "memory", name: "Memory", strategy: "run", customMemoryId: "" } },
        { id: "b", type: "task", position: { x: 0, y: 0 }, data: { kind: "task", name: "B", topic: "job.echo", promptTemplate: "", timeoutMs: 0, retries: 0 } },
      ],
      edges: [
        { id: "e1", source: "a", target: "m" },
        { id: "e2", source: "m", target: "b" },
      ],
    };
    expect(topoSortTaskNodes(wf).map((n) => n.id)).toEqual(["a", "b"]);
  });

  it("throws on cycles", () => {
    const wf: Workflow = {
      id: "wf",
      name: "wf",
      updatedAt: 0,
      nodes: [
        { id: "a", type: "task", position: { x: 0, y: 0 }, data: { kind: "task", name: "A", topic: "job.echo", promptTemplate: "", timeoutMs: 0, retries: 0 } },
        { id: "b", type: "task", position: { x: 10, y: 0 }, data: { kind: "task", name: "B", topic: "job.echo", promptTemplate: "", timeoutMs: 0, retries: 0 } },
      ],
      edges: [
        { id: "e1", source: "a", target: "b" },
        { id: "e2", source: "b", target: "a" },
      ],
    };
    expect(() => topoSortTaskNodes(wf)).toThrow(/cycle/i);
  });
});

describe("computeWorkflowOutput", () => {
  it("returns raw value for single placeholder", () => {
    const wf: Workflow = {
      id: "wf",
      name: "wf",
      updatedAt: 0,
      nodes: [
        {
          id: "out",
          type: "output",
          position: { x: 0, y: 0 },
          data: { kind: "output", name: "Output", outputs: [{ key: "final", template: "${prev.result}" }] },
        },
      ],
      edges: [],
    };
    const ctx: TemplateContext = { inputs: { prompt: "" }, nodeResults: {}, prevResult: { x: 1 } };
    expect(computeWorkflowOutput(wf, ctx)).toEqual({ final: { x: 1 } });
  });
});
