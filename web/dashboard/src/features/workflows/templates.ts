import { newID } from "../../lib/id";
import type { Workflow } from "./types";

function now() {
  return Date.now();
}

export function defaultWorkflows(): Workflow[] {
  const workflowId = newID("wf_");

  const inputId = newID("n_");
  const codeId = newID("n_");
  const explainId = newID("n_");
  const outputId = newID("n_");

  const echoWorkflowId = newID("wf_");
  const echoInputId = newID("n_");
  const echoStep1Id = newID("n_");
  const echoStep2Id = newID("n_");
  const echoOutputId = newID("n_");

  const chatWorkflowId = newID("wf_");
  const chatInputId = newID("n_");
  const draftId = newID("n_");
  const refineId = newID("n_");
  const summarizeId = newID("n_");
  const chatOutputId = newID("n_");

  const simpleChatWorkflowId = newID("wf_");
  const simpleChatInputId = newID("n_");
  const simpleDraftId = newID("n_");
  const simpleRefineId = newID("n_");
  const simpleSummarizeId = newID("n_");
  const simpleChatOutputId = newID("n_");

  return [
    {
      id: workflowId,
      name: "Code Review + Explain",
      updatedAt: now(),
      nodes: [
        {
          id: inputId,
          type: "input",
          position: { x: 50, y: 120 },
          data: {
            kind: "input",
            name: "Input",
            promptDefault: "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n",
            includeFilePath: true,
            filePathDefault: "src/main.go",
            includeInstruction: true,
            instructionDefault: "Refactor for readability and add basic error handling where appropriate.",
          },
        },
        {
          id: codeId,
          type: "task",
          position: { x: 320, y: 60 },
          data: {
            kind: "task",
            name: "Generate Patch",
            topic: "job.code.llm",
            timeoutMs: 120_000,
            retries: 0,
            promptTemplate:
              "${input.instruction}\n\nFile: ${input.filePath}\n\nCode:\n${input.prompt}\n\nReturn JSON with fields: patch, risks, summary.",
          },
        },
        {
          id: explainId,
          type: "task",
          position: { x: 320, y: 200 },
          data: {
            kind: "task",
            name: "Explain",
            topic: "job.chat.simple",
            timeoutMs: 120_000,
            retries: 0,
            promptTemplate:
              "Explain the following output in plain English. Summarize key changes and risks.\n\n${prev.result}",
          },
        },
        {
          id: outputId,
          type: "output",
          position: { x: 610, y: 130 },
          data: {
            kind: "output",
            name: "Output",
            outputs: [{ key: "final", template: "${prev.result}" }],
          },
        },
      ],
      edges: [
        { id: newID("e_"), source: inputId, target: codeId },
        { id: newID("e_"), source: codeId, target: explainId },
        { id: newID("e_"), source: explainId, target: outputId },
      ],
      variablesSchema: {},
    },
    {
      id: echoWorkflowId,
      name: "Dataflow Demo (Echo + Pointers)",
      updatedAt: now(),
      nodes: [
        {
          id: echoInputId,
          type: "input",
          position: { x: 50, y: 160 },
          data: {
            kind: "input",
            name: "Input",
            promptDefault: "Hello from coretexOS Studio workflow!",
            includeFilePath: false,
            filePathDefault: "",
            includeInstruction: true,
            instructionDefault: "Demonstrate edge-based dataflow and memory pointers.",
          },
        },
        {
          id: echoStep1Id,
          type: "task",
          position: { x: 320, y: 80 },
          data: {
            kind: "task",
            name: "Echo 1",
            topic: "job.echo",
            timeoutMs: 60_000,
            retries: 0,
            promptTemplate: "instruction=${input.instruction}\nmessage=${input.prompt}",
          },
        },
        {
          id: echoStep2Id,
          type: "task",
          position: { x: 320, y: 240 },
          data: {
            kind: "task",
            name: "Echo 2",
            topic: "job.echo",
            timeoutMs: 60_000,
            retries: 0,
            promptTemplate:
              "Upstream prompt:\n${prev.result.received_ctx.prompt}\n\nUpstream pointers:\nctx=${prev.context_ptr}\nres=${prev.result_ptr}",
          },
        },
        {
          id: echoOutputId,
          type: "output",
          position: { x: 610, y: 160 },
          data: {
            kind: "output",
            name: "Output",
            outputs: [
              { key: "step1_ctx_ptr", template: `\${node.${echoStep1Id}.context_ptr}` },
              { key: "step1_res_ptr", template: `\${node.${echoStep1Id}.result_ptr}` },
              { key: "step1_prompt", template: `\${node.${echoStep1Id}.result.received_ctx.prompt}` },
              { key: "step2_prompt", template: `\${node.${echoStep2Id}.result.received_ctx.prompt}` },
              { key: "final", template: "${prev.result}" },
            ],
          },
        },
      ],
      edges: [
        { id: newID("e_"), source: echoInputId, target: echoStep1Id },
        { id: newID("e_"), source: echoStep1Id, target: echoStep2Id },
        { id: newID("e_"), source: echoStep2Id, target: echoOutputId },
      ],
      variablesSchema: {},
    },
    {
      id: simpleChatWorkflowId,
      name: "Chat → Refine → Summarize (Simple)",
      updatedAt: now(),
      nodes: [
        {
          id: simpleChatInputId,
          type: "input",
          position: { x: 50, y: 180 },
          data: {
            kind: "input",
            name: "Input",
            promptDefault: "Explain how coretexOS uses ctx/res pointers for jobs.",
            includeFilePath: false,
            filePathDefault: "",
            includeInstruction: true,
            instructionDefault: "Answer concisely and include one example.",
          },
        },
        {
          id: simpleDraftId,
          type: "task",
          position: { x: 320, y: 60 },
          data: {
            kind: "task",
            name: "Draft",
            topic: "job.chat.simple",
            timeoutMs: 120_000,
            retries: 0,
            promptTemplate: "${input.instruction}\n\n${input.prompt}",
          },
        },
        {
          id: simpleRefineId,
          type: "task",
          position: { x: 320, y: 200 },
          data: {
            kind: "task",
            name: "Refine",
            topic: "job.chat.simple",
            timeoutMs: 120_000,
            retries: 0,
            promptTemplate:
              "Rewrite the answer to be clearer and more structured. Keep it concise.\n\nAnswer:\n${prev.result.response}",
          },
        },
        {
          id: simpleSummarizeId,
          type: "task",
          position: { x: 320, y: 340 },
          data: {
            kind: "task",
            name: "Summarize",
            topic: "job.chat.simple",
            timeoutMs: 120_000,
            retries: 0,
            promptTemplate: "Summarize the refined answer in 5 bullets.\n\nAnswer:\n${prev.result.response}",
          },
        },
        {
          id: simpleChatOutputId,
          type: "output",
          position: { x: 610, y: 220 },
          data: {
            kind: "output",
            name: "Output",
            outputs: [
              { key: "final", template: "${prev.result.response}" },
              { key: "ctx_ptr", template: "${prev.context_ptr}" },
              { key: "res_ptr", template: "${prev.result_ptr}" },
              { key: "raw", template: "${prev.result}" },
            ],
          },
        },
      ],
      edges: [
        { id: newID("e_"), source: simpleChatInputId, target: simpleDraftId },
        { id: newID("e_"), source: simpleDraftId, target: simpleRefineId },
        { id: newID("e_"), source: simpleRefineId, target: simpleSummarizeId },
        { id: newID("e_"), source: simpleSummarizeId, target: simpleChatOutputId },
      ],
      variablesSchema: {},
    },
    {
      id: chatWorkflowId,
      name: "Chat → Refine → Summarize (Advanced)",
      updatedAt: now(),
      nodes: [
        {
          id: chatInputId,
          type: "input",
          position: { x: 50, y: 180 },
          data: {
            kind: "input",
            name: "Input",
            promptDefault: "Explain how coretexOS uses memory pointers (ctx/res) and memory_id for chat history.",
            includeFilePath: false,
            filePathDefault: "",
            includeInstruction: true,
            instructionDefault: "Answer concisely, use practical examples, and keep it easy to scan.",
          },
        },
        {
          id: draftId,
          type: "task",
          position: { x: 320, y: 60 },
          data: {
            kind: "task",
            name: "Draft",
            topic: "job.chat.advanced",
            timeoutMs: 120_000,
            retries: 0,
            promptTemplate: "${input.instruction}\n\n${input.prompt}",
          },
        },
        {
          id: refineId,
          type: "task",
          position: { x: 320, y: 200 },
          data: {
            kind: "task",
            name: "Refine",
            topic: "job.chat.advanced",
            timeoutMs: 120_000,
            retries: 0,
            promptTemplate:
              "Rewrite the answer to be clearer and more structured. Keep it concise.\n\nAnswer:\n${prev.result.response}",
          },
        },
        {
          id: summarizeId,
          type: "task",
          position: { x: 320, y: 340 },
          data: {
            kind: "task",
            name: "Summarize",
            topic: "job.chat.advanced",
            timeoutMs: 120_000,
            retries: 0,
            promptTemplate: "Summarize the refined answer in 5 bullets.\n\nAnswer:\n${prev.result.response}",
          },
        },
        {
          id: chatOutputId,
          type: "output",
          position: { x: 610, y: 220 },
          data: {
            kind: "output",
            name: "Output",
            outputs: [
              { key: "final", template: "${prev.result.response}" },
              { key: "ctx_ptr", template: "${prev.context_ptr}" },
              { key: "res_ptr", template: "${prev.result_ptr}" },
              { key: "raw", template: "${prev.result}" },
            ],
          },
        },
      ],
      edges: [
        { id: newID("e_"), source: chatInputId, target: draftId },
        { id: newID("e_"), source: draftId, target: refineId },
        { id: newID("e_"), source: refineId, target: summarizeId },
        { id: newID("e_"), source: summarizeId, target: chatOutputId },
      ],
      variablesSchema: {},
    },
  ];
}
