import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Search } from "lucide-react";
import { useCommandPaletteStore } from "../state/commandPaletteStore";
import { useInspectorStore } from "../state/inspectorStore";
import { useStreamStore } from "../state/streamStore";
import { useWorkflowStore } from "../state/workflowStore";

type Command = {
  id: string;
  label: string;
  keywords?: string;
  hint?: string;
  action: () => void;
};

function normalize(text: string): string {
  return text.trim().toLowerCase();
}

export default function CommandPalette() {
  const open = useCommandPaletteStore((s) => s.open);
  const close = useCommandPaletteStore((s) => s.close);
  const navigate = useNavigate();
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [query, setQuery] = useState("");
  const [activeIdx, setActiveIdx] = useState(0);

  const commands = useMemo<Command[]>(
    () => [
      { id: "nav-dashboard", label: "Go to Dashboard", keywords: "nav dashboard", action: () => navigate("/dashboard") },
      { id: "nav-workflows", label: "Go to Workflows", keywords: "nav workflows", action: () => navigate("/workflows") },
      { id: "nav-runs", label: "Go to Runs", keywords: "nav runs", action: () => navigate("/runs") },
      { id: "nav-jobs", label: "Go to Jobs", keywords: "nav jobs", action: () => navigate("/jobs") },
      { id: "nav-traces", label: "Go to Traces", keywords: "nav traces", action: () => navigate("/traces") },
      { id: "nav-workers", label: "Go to Workers", keywords: "nav workers", action: () => navigate("/workers") },
      { id: "nav-chat", label: "Go to Chat", keywords: "nav chat", action: () => navigate("/chat") },
      { id: "nav-settings", label: "Go to Settings", keywords: "nav settings", action: () => navigate("/settings") },

      {
        id: "workflow-new",
        label: "New Workflow",
        keywords: "workflow create new",
        hint: "Creates local workflow",
        action: () => {
          useWorkflowStore.getState().createWorkflow();
          navigate("/workflows");
        },
      },
      {
        id: "inspector-toggle",
        label: "Toggle Inspector",
        keywords: "inspector toggle panel",
        action: () => useInspectorStore.getState().toggle(),
      },
      {
        id: "stream-clear",
        label: "Clear Live Feed",
        keywords: "stream ws feed clear",
        action: () => useStreamStore.getState().clear(),
      },
    ],
    [navigate],
  );

  const filtered = useMemo(() => {
    const q = normalize(query);
    if (!q) {
      return commands;
    }
    return commands.filter((c) => normalize(`${c.label} ${c.keywords || ""}`).includes(q));
  }, [commands, query]);

  useEffect(() => {
    if (!open) {
      return;
    }
    setQuery("");
    setActiveIdx(0);
    setTimeout(() => inputRef.current?.focus(), 0);
  }, [open]);

  useEffect(() => {
    if (!open) {
      return;
    }
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        close();
        return;
      }
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setActiveIdx((i) => Math.min(filtered.length - 1, i + 1));
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        setActiveIdx((i) => Math.max(0, i - 1));
        return;
      }
      if (e.key === "Enter") {
        e.preventDefault();
        const cmd = filtered[activeIdx];
        if (cmd) {
          close();
          cmd.action();
        }
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [activeIdx, close, filtered, open]);

  if (!open) {
    return null;
  }

  const active = filtered[activeIdx];

  return (
    <div className="fixed inset-0 z-[60] flex items-start justify-center bg-black/60 p-6" onClick={close}>
      <div
        className="glass w-full max-w-[760px] rounded-2xl border border-primary-border shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-primary-border px-4 py-3">
          <Search className="h-4 w-4 text-tertiary-text" />
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setActiveIdx(0);
            }}
            placeholder="Type a command…"
            className="w-full bg-transparent text-sm text-primary-text outline-none placeholder:text-tertiary-text"
          />
          <div className="text-[11px] text-tertiary-text">Esc</div>
        </div>

        <div className="max-h-[420px] overflow-auto p-2">
          {filtered.length === 0 ? (
            <div className="px-3 py-8 text-center text-sm text-tertiary-text">No matches.</div>
          ) : (
            filtered.map((c, idx) => (
              <button
                key={c.id}
                type="button"
                onClick={() => {
                  close();
                  c.action();
                }}
                onMouseEnter={() => setActiveIdx(idx)}
                className={[
                  "flex w-full items-center justify-between rounded-xl px-3 py-2 text-left text-sm",
                  idx === activeIdx ? "bg-tertiary-background text-primary-text" : "text-secondary-text hover:bg-tertiary-background",
                ].join(" ")}
              >
                <span>{c.label}</span>
                <span className="text-xs text-tertiary-text">{c.hint || ""}</span>
              </button>
            ))
          )}
        </div>

        {active?.hint ? (
          <div className="border-t border-primary-border px-4 py-2 text-xs text-tertiary-text">{active.hint}</div>
        ) : null}
      </div>
    </div>
  );
}

