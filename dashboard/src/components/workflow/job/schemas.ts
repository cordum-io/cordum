import { z } from "zod";

// ---------------------------------------------------------------------------
// Agent Task — AI agent job with prompt, tokens, memory
// ---------------------------------------------------------------------------

export const agentTaskSchema = z.object({
  label: z.string().min(1, "Name required"),
  topic: z.string().min(1, "Topic required"),
  prompt: z.string().optional(),
  adapterId: z.string().optional(),
  priority: z.string().optional(),
  maxInputTokens: z.coerce.number().int().min(0).max(1_000_000).optional(),
  maxOutputTokens: z.coerce.number().int().min(0).max(1_000_000).optional(),
  maxTotalTokens: z.coerce.number().int().min(0).max(1_000_000).optional(),
  deadlineMs: z.coerce.number().int().min(0).optional(),
  allowSummarization: z.boolean().optional(),
  allowRetrieval: z.boolean().optional(),
  memoryId: z.string().optional(),
  contextMode: z.string().optional(),
  timeout: z.string().optional(),
  retryMax: z.coerce.number().int().min(0).optional(),
});

export type AgentTaskConfig = z.infer<typeof agentTaskSchema>;

// ---------------------------------------------------------------------------
// Pack Action — invoke a pack's capability
// ---------------------------------------------------------------------------

export const packActionSchema = z.object({
  label: z.string().min(1, "Name required"),
  packId: z.string().min(1, "Pack ID required"),
  topic: z.string().optional(),
  capability: z.string().optional(),
  input: z.string().optional(), // JSON string
  riskTags: z.array(z.string()).optional(),
  requires: z.array(z.string()).optional(),
  timeout: z.string().optional(),
  retryMax: z.coerce.number().int().min(0).optional(),
});

export type PackActionConfig = z.infer<typeof packActionSchema>;

// ---------------------------------------------------------------------------
// Tool Call — invoke a specific capability/tool
// ---------------------------------------------------------------------------

export const toolCallSchema = z.object({
  label: z.string().min(1, "Name required"),
  capability: z.string().min(1, "Capability required"),
  prompt: z.string().optional(),
  riskTags: z.array(z.string()).optional(),
  topic: z.string().optional(),
  priority: z.string().optional(),
  timeout: z.string().optional(),
  retryMax: z.coerce.number().int().min(0).optional(),
  labels: z.record(z.string(), z.string()).optional(),
});

export type ToolCallConfig = z.infer<typeof toolCallSchema>;
