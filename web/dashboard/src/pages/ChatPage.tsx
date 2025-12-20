import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import Card from "../components/Card";
import EmptyState from "../components/EmptyState";
import Loading from "../components/Loading";
import JsonViewer from "../components/JsonViewer";
import MemoryPointerViewer from "../components/MemoryPointerViewer";
import { fetchJob, submitJob, type JobDetailResponse, type SubmitJobResponse } from "../lib/api";
import { useAuthStore } from "../state/authStore";
import { Link } from "react-router-dom";
import { useInspectorStore } from "../state/inspectorStore";

type ChatLine = { role: "user" | "assistant"; text: string };

type PersistedChatStateV1 = {
  memory_id: string;
  lines: ChatLine[];
};

const chatStateStorageKey = "coretex.chat.state.v1";

function loadChatState(): PersistedChatStateV1 | null {
  if (typeof window === "undefined") {
    return null;
  }
  const raw = window.localStorage.getItem(chatStateStorageKey);
  if (!raw) {
    return null;
  }
  try {
    const parsed = JSON.parse(raw) as PersistedChatStateV1;
    if (!parsed || typeof parsed !== "object") return null;
    if (typeof parsed.memory_id !== "string" || !Array.isArray(parsed.lines)) return null;
    return parsed;
  } catch {
    return null;
  }
}

function persistChatState(state: PersistedChatStateV1) {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(chatStateStorageKey, JSON.stringify(state));
  } catch {
    // ignore quota / storage errors
  }
}

function generateMemoryID(): string {
  const uuid =
    typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `session:${uuid}`;
}

function isTerminalJobState(state: JobDetailResponse["state"]): boolean {
  return state === "SUCCEEDED" || state === "FAILED" || state === "CANCELLED" || state === "TIMEOUT" || state === "DENIED";
}

function extractAssistantText(result: unknown): string | null {
  if (!result) return null;
  if (typeof result === "string") return result;
  const r = result as any;
  if (typeof r.response === "string") return r.response;
  if (typeof r.output === "string") return r.output;
  if (typeof r.error?.message === "string") return r.error.message;
  return null;
}

function buildChatPrompt(lines: ChatLine[], opts?: { maxLines?: number }): string {
  const maxLines = Math.max(1, opts?.maxLines ?? 24);
  const slice = lines.slice(-maxLines);
  const transcript = slice.map((l) => `${l.role}: ${l.text}`.trim()).join("\n").trim();
  if (!transcript) {
    return "assistant:";
  }
  return `${transcript}\nassistant:`;
}

const defaultTopics = [
  "job.chat.simple",
  "job.chat.advanced",
  "job.code.llm",
  "job.echo",
  "job.workflow.demo",
];

export default function ChatPage() {
  const [topic, setTopic] = useState("job.chat.simple");
  const [prompt, setPrompt] = useState("");
  const [activeJobId, setActiveJobId] = useState<string | null>(null);
  const [activeTraceId, setActiveTraceId] = useState<string | null>(null);
  const [lastJob, setLastJob] = useState<JobDetailResponse | null>(null);
  const [lastTraceId, setLastTraceId] = useState<string | null>(null);
  const [memoryId, setMemoryId] = useState<string>(() => loadChatState()?.memory_id ?? generateMemoryID());
  const [lines, setLines] = useState<ChatLine[]>(() => loadChatState()?.lines ?? []);
  const lastAppendedJobIdRef = useRef<string | null>(null);
  const authStatus = useAuthStore((s) => s.status);
  const canPoll = authStatus === "authorized" || authStatus === "unknown";
  const showInspector = useInspectorStore((s) => s.show);

  useEffect(() => {
    persistChatState({ memory_id: memoryId, lines });
  }, [lines, memoryId]);

  const submitM = useMutation({
    mutationFn: submitJob,
    onSuccess: (resp: SubmitJobResponse) => {
      setActiveJobId(resp.job_id);
      setActiveTraceId(resp.trace_id);
    },
  });

  const jobQ = useQuery({
    queryKey: ["job", activeJobId, authStatus],
    queryFn: () => fetchJob(activeJobId!),
    enabled: Boolean(activeJobId),
    refetchInterval: canPoll ? 1_000 : false,
  });

  useEffect(() => {
    if (!activeJobId) return;
    const job = jobQ.data;
    if (!job) return;
    if (job.id !== activeJobId) return;
    if (!isTerminalJobState(job.state)) return;
    if (lastAppendedJobIdRef.current === activeJobId) return;

    lastAppendedJobIdRef.current = activeJobId;
    setLastJob(job);
    setLastTraceId(activeTraceId);
    setActiveJobId(null);
    setActiveTraceId(null);

    const text = extractAssistantText(job.result) ?? (job.state === "SUCCEEDED" ? "" : `Job finished with state=${job.state}`);
    if (text.trim()) {
      setLines((cur) => [...cur, { role: "assistant", text }]);
    }
  }, [activeJobId, activeTraceId, jobQ.data]);

  const onSend = () => {
    if (authStatus === "missing_api_key" || authStatus === "invalid_api_key") {
      return;
    }
    if (submitM.isPending || activeJobId) {
      return;
    }
    const p = prompt.trim();
    if (!p) return;
    const nextLines: ChatLine[] = [...lines, { role: "user", text: p }];
    setLines(nextLines);
    setPrompt("");
    setLastJob(null);
    setLastTraceId(null);
    lastAppendedJobIdRef.current = null;

    const contextMode = topic.startsWith("job.chat.") ? "chat" : topic.startsWith("job.code.") ? "rag" : undefined;
    const includeHistoryInPrompt = topic === "job.chat.simple";
    const promptToSend = includeHistoryInPrompt ? buildChatPrompt(nextLines) : p;
    submitM.mutate({ topic, prompt: promptToSend, memory_id: memoryId, context_mode: contextMode });
  };

  const status = submitM.isPending
    ? "submitting..."
    : activeJobId
      ? `job_id=${activeJobId}${activeTraceId ? ` trace_id=${activeTraceId}` : ""}`
      : lastJob
        ? `last_job=${lastJob.id} (${lastJob.state})${lastTraceId ? ` trace_id=${lastTraceId}` : ""}`
        : "";
  const jobLinkId = activeJobId || lastJob?.id || "";
  const traceLinkId = activeTraceId || lastTraceId || "";

  return (
    <div className="space-y-6">
      <Card title="Chat">
        {authStatus === "missing_api_key" || authStatus === "invalid_api_key" ? (
          <div className="mb-4 rounded-xl border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-200">
            {authStatus === "missing_api_key"
              ? "Gateway requires an API key."
              : "API key was rejected."}{" "}
            <Link to="/settings" className="underline">
              Open Settings
            </Link>
            .
          </div>
        ) : null}
        <div className="flex items-center gap-2">
          <select
            value={topic}
            onChange={(e) => setTopic(e.target.value)}
            className="rounded-lg border border-primary-border bg-secondary-background px-3 py-2 text-xs text-primary-text"
          >
            {defaultTopics.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
          <div className="text-xs text-tertiary-text">
            {status}
          </div>
          {jobLinkId ? (
            <Link
              to={`/jobs/${encodeURIComponent(jobLinkId)}`}
              className="text-xs font-mono text-secondary-text hover:underline"
            >
              open job
            </Link>
          ) : null}
          {traceLinkId ? (
            <Link
              to={`/traces?trace_id=${encodeURIComponent(traceLinkId)}`}
              className="text-xs font-mono text-secondary-text hover:underline"
            >
              open trace
            </Link>
          ) : null}
          {lastJob ? (
            <>
              <button
                type="button"
                className="rounded-md border border-white/10 bg-black/20 px-2 py-1 text-[10px] text-zinc-300 hover:bg-black/30"
                onClick={() =>
                  showInspector("Memory: Context Pointer", <MemoryPointerViewer pointer={lastJob.context_ptr || `redis://ctx:${lastJob.id}`} />)
                }
                title={lastJob.context_ptr || `redis://ctx:${lastJob.id}`}
              >
                ctx
              </button>
              <button
                type="button"
                disabled={!lastJob.result_ptr}
                className="rounded-md border border-white/10 bg-black/20 px-2 py-1 text-[10px] text-zinc-300 hover:bg-black/30"
                onClick={() =>
                  showInspector("Memory: Result Pointer", <MemoryPointerViewer pointer={lastJob.result_ptr!} />)
                }
                title={lastJob.result_ptr || `redis://res:${lastJob.id}`}
              >
                res
              </button>
            </>
          ) : null}
          <div className="ml-auto flex items-center gap-2">
            <div className="hidden max-w-[360px] truncate font-mono text-[11px] text-tertiary-text md:block" title={memoryId}>
              memory_id={memoryId}
            </div>
            <button
              type="button"
              className="rounded-md border border-white/10 bg-black/20 px-2 py-1 text-[10px] text-zinc-300 hover:bg-black/30"
              onClick={() =>
                showInspector("Memory: Chat History (context engine)", <MemoryPointerViewer pointer={`redis://mem:${memoryId}:events`} />)
              }
              title={`redis://mem:${memoryId}:events`}
            >
              mem
            </button>
            <button
              type="button"
              className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-[11px] text-secondary-text hover:bg-tertiary-background"
              onClick={() => {
                setMemoryId(generateMemoryID());
                setLines([]);
                setPrompt("");
                setActiveJobId(null);
                setActiveTraceId(null);
                setLastJob(null);
                setLastTraceId(null);
                lastAppendedJobIdRef.current = null;
              }}
            >
              New chat
            </button>
          </div>
        </div>

        <div className="mt-4 space-y-3">
          {lines.length === 0 ? (
            <EmptyState title="Send a message" description="This submits `POST /api/v1/jobs`. Chat topics use `memory_id` so each message can build on prior context." />
          ) : (
            lines.map((l, idx) => (
              <div
                key={idx}
                className={[
                  "max-w-[85%] rounded-2xl border border-primary-border px-4 py-3 text-sm",
                  l.role === "user"
                    ? "ml-auto bg-secondary-background text-primary-text"
                    : "bg-tertiary-background text-primary-text",
                ].join(" ")}
              >
                {l.text}
              </div>
            ))
          )}

          {jobQ.isLoading && activeJobId ? <Loading label="Waiting for job result..." /> : null}
          {jobQ.isError && activeJobId ? (
            <EmptyState title="Failed to load job" description="Check API base/key in Settings, then try again." />
          ) : null}
        </div>

        <div className="mt-4 flex items-end gap-2">
          <textarea
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            placeholder="Type a prompt..."
            className="min-h-[96px] flex-1 resize-none rounded-xl border border-primary-border bg-secondary-background p-3 text-sm text-primary-text placeholder:text-tertiary-text"
          />
          <button
            onClick={onSend}
            disabled={authStatus === "missing_api_key" || authStatus === "invalid_api_key" || submitM.isPending || Boolean(activeJobId)}
            className={[
              "rounded-xl border px-4 py-3 text-sm font-semibold",
              authStatus === "missing_api_key" || authStatus === "invalid_api_key" || submitM.isPending || Boolean(activeJobId)
                ? "cursor-not-allowed border-primary-border bg-secondary-background/50 text-tertiary-text"
                : "border-primary-border bg-secondary-background text-primary-text hover:bg-tertiary-background",
            ].join(" ")}
          >
            Send
          </button>
        </div>

        {lastJob?.result ? (
          <div className="mt-4">
            <Card title="Raw Result">
              <JsonViewer value={lastJob.result} />
            </Card>
          </div>
        ) : null}
      </Card>
    </div>
  );
}
