export type ChatMessageRole = "user" | "agent" | "system";

export type ChatMessage = {
  id: string;
  run_id: string;
  role: ChatMessageRole;
  content: string;
  step_id?: string;
  job_id?: string;
  agent_id?: string;
  agent_name?: string;
  created_at: string;
  metadata?: Record<string, unknown>;
};

export type ChatThread = {
  run_id: string;
  messages: ChatMessage[];
  status: "active" | "paused" | "completed";
};

export type ChatResponse = {
  items: ChatMessage[];
  next_cursor?: number | null;
};
