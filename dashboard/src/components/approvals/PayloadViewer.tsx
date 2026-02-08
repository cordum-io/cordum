import { useState } from "react";
import { AlertTriangle, ChevronDown, ChevronRight, Copy, Check, Mail } from "lucide-react";
import { cn } from "../../lib/utils";

// ---------------------------------------------------------------------------
// Job type detection
// ---------------------------------------------------------------------------

type JobType = "database" | "api" | "communication" | "file" | "generic";

function detectJobType(topic?: string, capabilities?: string[]): JobType {
  const t = (topic ?? "").toLowerCase();
  const caps = new Set((capabilities ?? []).map((c) => c.toLowerCase()));

  if (t.includes("email") || t.includes("slack") || t.includes("notify") || t.includes("notification")) {
    return "communication";
  }
  if (t.includes("db") || t.includes("sql") || caps.has("write")) {
    return "database";
  }
  if (t.includes("http") || t.includes("api")) {
    return "api";
  }
  if (caps.has("file_write") || caps.has("file_delete")) {
    return "file";
  }
  return "generic";
}

function isDestructive(topic?: string, capabilities?: string[]): boolean {
  const t = (topic ?? "").toLowerCase();
  const caps = new Set((capabilities ?? []).map((c) => c.toLowerCase()));
  return (
    t.includes("delete") ||
    t.includes("drop") ||
    t.includes("truncate") ||
    t.includes("destroy") ||
    caps.has("destructive")
  );
}

// ---------------------------------------------------------------------------
// Copy button (shared)
// ---------------------------------------------------------------------------

function CopyButton({ text, className }: { text: string; className?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      className={cn(
        "inline-flex items-center rounded p-1 text-muted hover:text-ink transition-colors",
        className,
      )}
      onClick={() => {
        navigator.clipboard.writeText(text);
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      }}
      aria-label="Copy to clipboard"
    >
      {copied ? <Check className="h-3.5 w-3.5 text-success" /> : <Copy className="h-3.5 w-3.5" />}
    </button>
  );
}

// ---------------------------------------------------------------------------
// JSON syntax coloring
// ---------------------------------------------------------------------------

function JsonValue({ value }: { value: unknown }) {
  if (value === null) return <span className="text-gray-400">null</span>;
  if (typeof value === "boolean") return <span className="text-purple-600 dark:text-purple-400">{String(value)}</span>;
  if (typeof value === "number") return <span className="text-blue-600 dark:text-blue-400">{value}</span>;
  if (typeof value === "string") return <span className="text-green-700 dark:text-green-400">&quot;{value}&quot;</span>;
  return null;
}

// ---------------------------------------------------------------------------
// Recursive collapsible JSON node
// ---------------------------------------------------------------------------

const DEFAULT_EXPAND_DEPTH = 2;

function JsonNode({
  keyName,
  value,
  depth = 0,
}: {
  keyName?: string;
  value: unknown;
  depth?: number;
}) {
  const isObj = value !== null && typeof value === "object" && !Array.isArray(value);
  const isArr = Array.isArray(value);
  const isCollapsible = isObj || isArr;

  const [expanded, setExpanded] = useState(depth < DEFAULT_EXPAND_DEPTH);

  if (!isCollapsible) {
    return (
      <div className="flex" style={{ paddingLeft: depth * 16 }}>
        {keyName != null && (
          <span className="text-ink font-medium">{keyName}: </span>
        )}
        <JsonValue value={value} />
      </div>
    );
  }

  const entries = isArr
    ? (value as unknown[]).map((v, i) => [String(i), v] as const)
    : Object.entries(value as Record<string, unknown>);
  const bracketOpen = isArr ? "[" : "{";
  const bracketClose = isArr ? "]" : "}";

  return (
    <div>
      <button
        type="button"
        className="flex items-center gap-0.5 hover:bg-surface2/50 rounded px-0.5 -ml-0.5"
        style={{ paddingLeft: depth * 16 }}
        onClick={() => setExpanded((v) => !v)}
      >
        {expanded ? (
          <ChevronDown className="h-3 w-3 text-muted shrink-0" />
        ) : (
          <ChevronRight className="h-3 w-3 text-muted shrink-0" />
        )}
        {keyName != null && (
          <span className="text-ink font-medium">{keyName}: </span>
        )}
        <span className="text-muted">
          {bracketOpen}
          {!expanded && (
            <span className="text-muted">
              {` ${entries.length} ${isArr ? "items" : "keys"} `}
            </span>
          )}
          {!expanded && bracketClose}
        </span>
      </button>
      {expanded && (
        <>
          {entries.map(([k, v]) => (
            <JsonNode key={k} keyName={isArr ? undefined : k} value={v} depth={depth + 1} />
          ))}
          <div style={{ paddingLeft: depth * 16 }} className="text-muted">
            {bracketClose}
          </div>
        </>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// GenericJsonViewer
// ---------------------------------------------------------------------------

function GenericJsonViewer({ data }: { data: Record<string, unknown> }) {
  const jsonText = JSON.stringify(data, null, 2);

  return (
    <div className="relative rounded-xl border border-border bg-surface2/50 p-4 max-h-96 overflow-auto">
      <div className="absolute top-2 right-2">
        <CopyButton text={jsonText} />
      </div>
      <div className="font-mono text-xs leading-relaxed">
        <JsonNode value={data} />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// CommunicationRenderer
// ---------------------------------------------------------------------------

function CommunicationRenderer({ data }: { data: Record<string, unknown> }) {
  const recipients = (data.recipients ?? data.to ?? data.channel) as
    | string
    | string[]
    | undefined;
  const subject = (data.subject ?? data.channel) as string | undefined;
  const body = (data.message ?? data.body ?? data.content) as string | undefined;

  // Fall back to generic if expected fields are missing
  if (!recipients && !subject && !body) {
    return <GenericJsonViewer data={data} />;
  }

  const recipientStr = Array.isArray(recipients) ? recipients.join(", ") : recipients;

  return (
    <div className="rounded-xl border border-border bg-surface2/50 overflow-hidden">
      <div className="flex items-center gap-2 border-b border-border bg-surface px-4 py-2.5">
        <Mail className="h-4 w-4 text-accent" />
        <span className="text-xs font-semibold text-ink">Message Preview</span>
      </div>
      <div className="space-y-2 p-4 text-xs">
        {recipientStr && (
          <p>
            <span className="text-muted">To: </span>
            <span className="font-medium text-ink">{recipientStr}</span>
          </p>
        )}
        {subject && (
          <p>
            <span className="text-muted">Subject: </span>
            <span className="font-medium text-ink">{subject}</span>
          </p>
        )}
        {body && (
          <div className="mt-2 rounded-lg border border-border bg-surface p-3 text-sm text-ink whitespace-pre-wrap">
            {body}
          </div>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Destructive operation warning
// ---------------------------------------------------------------------------

function DestructiveWarning({ topic }: { topic?: string }) {
  return (
    <div className="flex items-start gap-2.5 rounded-lg border border-red-200 dark:border-red-800 bg-red-50 dark:bg-red-950 p-3">
      <AlertTriangle className="h-4 w-4 shrink-0 text-red-600 dark:text-red-400 mt-0.5" />
      <div className="text-xs">
        <p className="font-semibold text-red-800 dark:text-red-300">Destructive Operation</p>
        {topic && (
          <p className="mt-0.5 text-red-700 dark:text-red-400">Topic: {topic}</p>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// PayloadViewer (main export)
// ---------------------------------------------------------------------------

interface PayloadViewerProps {
  jobContext?: Record<string, unknown>;
  topic?: string;
  capabilities?: string[];
}

export function PayloadViewer({ jobContext, topic, capabilities }: PayloadViewerProps) {
  if (!jobContext || Object.keys(jobContext).length === 0) {
    return <p className="text-xs text-muted">No payload data available.</p>;
  }

  const destructive = isDestructive(topic, capabilities);
  const jobType = detectJobType(topic, capabilities);

  return (
    <div className="space-y-3">
      {destructive && <DestructiveWarning topic={topic} />}
      {jobType === "communication" ? (
        <CommunicationRenderer data={jobContext} />
      ) : (
        <GenericJsonViewer data={jobContext} />
      )}
    </div>
  );
}
