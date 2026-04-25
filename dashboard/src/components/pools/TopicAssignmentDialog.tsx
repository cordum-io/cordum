import { useState } from "react";
import { ConfirmDialog } from "../ui/ConfirmDialog";
import { Link2, X } from "lucide-react";

interface TopicAssignmentDialogProps {
  open: boolean;
  onClose: () => void;
  onAddTopic: (topic: string) => void;
  onRemoveTopic: (topic: string) => void;
  poolName: string;
  topics: string[];
  isAdding?: boolean;
  isRemoving?: boolean;
}

const TOPIC_RE = /^job\.[a-zA-Z0-9_-]+(\.[a-zA-Z0-9_*-]+)*$/;

export function TopicAssignmentDialog({ open, onClose, onAddTopic, onRemoveTopic, poolName, topics, isAdding, isRemoving }: TopicAssignmentDialogProps) {
  const [newTopic, setNewTopic] = useState("");
  const [error, setError] = useState("");

  const handleAdd = () => {
    // Guard against double-fire: the Enter-key path can call this even
    // while the Add button is disabled mid-add (`isAdding`), which can
    // submit the same topic twice.
    if (isAdding) return;
    const trimmed = newTopic.trim();
    if (!trimmed) return;
    if (!TOPIC_RE.test(trimmed)) {
      setError("Topic must match job.* pattern");
      return;
    }
    if (topics.includes(trimmed)) {
      setError("Topic already mapped");
      return;
    }
    setError("");
    onAddTopic(trimmed);
    setNewTopic("");
  };

  return (
    <ConfirmDialog
      open={open}
      onClose={() => { setNewTopic(""); setError(""); onClose(); }}
      onConfirm={() => { setNewTopic(""); setError(""); onClose(); }}
      title={`Topics: ${poolName}`}
      icon={Link2}
      confirmLabel="Done"
      description={
        <div className="space-y-3 mt-2">
          {topics.length === 0 ? (
            <p className="text-xs text-muted-foreground italic">No topics mapped to this pool.</p>
          ) : (
            <div className="space-y-1 max-h-40 overflow-y-auto">
              {topics.map((topic) => (
                <div key={topic} className="flex items-center justify-between rounded-xl bg-surface-0 px-3 py-1.5">
                  <span className="text-xs font-mono text-foreground">{topic}</span>
                  <button
                    type="button"
                    onClick={() => onRemoveTopic(topic)}
                    disabled={isRemoving}
                    className="p-0.5 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors disabled:opacity-50"
                    title="Remove topic"
                  >
                    <X className="w-3 h-3" />
                  </button>
                </div>
              ))}
            </div>
          )}
          <div className="flex gap-2">
            <input
              type="text"
              value={newTopic}
              onChange={(e) => { setNewTopic(e.target.value); setError(""); }}
              onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); handleAdd(); } }}
              placeholder="job.my-service.process"
              className="flex-1 h-8 px-3 text-xs bg-surface-0 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground/40 focus:outline-none focus:ring-1 focus:ring-cordum font-mono"
            />
            <button
              type="button"
              onClick={handleAdd}
              disabled={isAdding || !newTopic.trim()}
              className="h-8 px-3 text-xs font-medium rounded-full bg-cordum text-surface-0 hover:bg-cordum-dim transition-colors disabled:opacity-50"
            >
              Add
            </button>
          </div>
          {error && <p className="text-xs text-destructive">{error}</p>}
        </div>
      }
    />
  );
}

