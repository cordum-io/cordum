import { useInspectorStore } from "../state/inspectorStore";

export default function Inspector() {
  const open = useInspectorStore((s) => s.open);
  const title = useInspectorStore((s) => s.title);
  const body = useInspectorStore((s) => s.body);
  const close = useInspectorStore((s) => s.close);

  if (!open) {
    return null;
  }

  return (
    <aside className="glass w-[360px] border-l border-primary-border">
      <div className="flex items-center justify-between border-b border-primary-border px-4 py-3">
        <div className="text-sm font-semibold text-primary-text">{title || "Inspector"}</div>
        <button
          onClick={close}
          className="rounded-lg border border-primary-border bg-secondary-background px-2 py-1 text-xs text-secondary-text hover:bg-tertiary-background"
        >
          Close
        </button>
      </div>
      <div className="p-4 text-sm text-primary-text">{body}</div>
    </aside>
  );
}

