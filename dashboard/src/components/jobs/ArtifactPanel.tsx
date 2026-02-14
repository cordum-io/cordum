import { useEffect, useMemo, useState } from "react";
import Editor, { loader } from "@monaco-editor/react";
import {
  AlertTriangle,
  Download,
  FileCode,
  FileJson,
  FileSearch,
  Image as ImageIcon,
  Loader2,
  PackageOpen,
} from "lucide-react";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { Card, CardHeader, CardTitle } from "../ui/Card";
import { EmptyState } from "../ui/EmptyState";
import { useArtifact, useJobArtifacts } from "../../hooks/useMemory";
import type { JobArtifactRef } from "../../api/types";

const MONACO_BASE_PATH = "/monaco/vs";
loader.config({ paths: { vs: MONACO_BASE_PATH } });

interface ArtifactPanelProps {
  jobId: string;
}

function formatBytes(value?: number): string {
  const bytes = value ?? 0;
  if (!Number.isFinite(bytes) || bytes <= 0) return "-";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

function formatTimestamp(raw?: string): string {
  if (!raw) return "-";
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) return raw;
  return date.toLocaleString();
}

function truncatePointer(ptr: string): string {
  if (ptr.length <= 44) return ptr;
  return `${ptr.slice(0, 20)}...${ptr.slice(-18)}`;
}

function decodeBase64ToBytes(encoded: string): Uint8Array {
  const binary = atob(encoded);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}

function decodeUtf8(bytes: Uint8Array): string {
  return new TextDecoder().decode(bytes);
}

function isLikelyJson(contentType: string, text: string): boolean {
  if (contentType.includes("json")) return true;
  const trimmed = text.trim();
  return trimmed.startsWith("{") || trimmed.startsWith("[");
}

function inferLanguage(contentType: string, ptr: string): string {
  const normalizedType = contentType.toLowerCase();
  if (normalizedType.includes("json")) return "json";
  if (normalizedType.includes("yaml") || normalizedType.includes("yml")) return "yaml";
  if (normalizedType.includes("javascript")) return "javascript";
  if (normalizedType.includes("typescript")) return "typescript";
  if (normalizedType.includes("python")) return "python";
  if (normalizedType.includes("go")) return "go";
  if (normalizedType.includes("html")) return "html";
  if (normalizedType.includes("css")) return "css";
  if (normalizedType.includes("xml")) return "xml";
  if (normalizedType.includes("shell")) return "shell";

  const lowerPtr = ptr.toLowerCase();
  if (lowerPtr.endsWith(".ts")) return "typescript";
  if (lowerPtr.endsWith(".tsx")) return "typescript";
  if (lowerPtr.endsWith(".js")) return "javascript";
  if (lowerPtr.endsWith(".py")) return "python";
  if (lowerPtr.endsWith(".go")) return "go";
  if (lowerPtr.endsWith(".json")) return "json";
  if (lowerPtr.endsWith(".yml") || lowerPtr.endsWith(".yaml")) return "yaml";
  if (lowerPtr.endsWith(".md")) return "markdown";
  if (lowerPtr.endsWith(".sh")) return "shell";
  return "plaintext";
}

function looksLikeText(contentType: string): boolean {
  if (contentType.startsWith("text/")) return true;
  return (
    contentType.includes("json") ||
    contentType.includes("yaml") ||
    contentType.includes("xml") ||
    contentType.includes("javascript") ||
    contentType.includes("typescript") ||
    contentType.includes("python") ||
    contentType.includes("shell") ||
    contentType.includes("markdown")
  );
}

function pointerFilename(ptr: string, contentType: string): string {
  const clean = ptr.replace(/^redis:\/\//, "");
  const compact = clean.replace(/[^a-zA-Z0-9._-]/g, "_");
  if (compact.includes(".")) return compact;
  if (contentType.includes("json")) return `${compact}.json`;
  if (contentType.startsWith("text/")) return `${compact}.txt`;
  if (contentType.includes("yaml")) return `${compact}.yaml`;
  return compact;
}

function artifactTypeVariant(contentType: string): "default" | "info" | "success" | "warning" {
  if (contentType.startsWith("image/")) return "success";
  if (contentType.includes("json")) return "info";
  if (contentType.startsWith("text/")) return "warning";
  return "default";
}

function toDownloadableBlob(bytes: Uint8Array, contentType: string): Blob {
  const copy = new Uint8Array(bytes.byteLength);
  copy.set(bytes);
  return new Blob([copy], { type: contentType || "application/octet-stream" });
}

function downloadContent(ptr: string, contentType: string, bytes: Uint8Array) {
  const blob = toDownloadableBlob(bytes, contentType);
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = pointerFilename(ptr, contentType);
  link.click();
  URL.revokeObjectURL(url);
}

function JsonNode({
  value,
  name,
  depth = 0,
}: {
  value: unknown;
  name?: string;
  depth?: number;
}) {
  if (value === null || value === undefined) {
    return (
      <div className="text-xs">
        {name ? <span className="text-muted">{name}: </span> : null}
        <span className="text-muted">null</span>
      </div>
    );
  }

  if (typeof value !== "object") {
    return (
      <div className="text-xs">
        {name ? <span className="text-muted">{name}: </span> : null}
        <span className="text-ink">{String(value)}</span>
      </div>
    );
  }

  if (Array.isArray(value)) {
    return (
      <details open={depth < 1} className="text-xs">
        <summary className="cursor-pointer text-muted">
          {name ? `${name}: ` : ""}[{value.length}]
        </summary>
        <div className="ml-4 mt-1 space-y-1 border-l border-border/50 pl-2">
          {value.map((item, index) => (
            <JsonNode key={`${name ?? "arr"}-${index}`} value={item} name={String(index)} depth={depth + 1} />
          ))}
        </div>
      </details>
    );
  }

  const entries = Object.entries(value as Record<string, unknown>);
  return (
    <details open={depth < 1} className="text-xs">
      <summary className="cursor-pointer text-muted">
        {name ? `${name}: ` : ""}
        {`{${entries.length} keys}`}
      </summary>
      <div className="ml-4 mt-1 space-y-1 border-l border-border/50 pl-2">
        {entries.map(([key, item]) => (
          <JsonNode key={`${name ?? "obj"}-${key}`} value={item} name={key} depth={depth + 1} />
        ))}
      </div>
    </details>
  );
}

function ArtifactViewer({
  ptr,
  contentType,
  contentBase64,
  sizeBytes,
}: {
  ptr: string;
  contentType: string;
  contentBase64: string;
  sizeBytes?: number;
}) {
  const [decodedBytes, decodedText, decodedJson] = useMemo(() => {
    let bytes: Uint8Array;
    let text = "";
    try {
      bytes = decodeBase64ToBytes(contentBase64);
      text = decodeUtf8(bytes);
    } catch {
      bytes = new Uint8Array();
    }
    if (!isLikelyJson(contentType, text)) {
      return [bytes, text, null] as const;
    }
    try {
      return [bytes, text, JSON.parse(text)] as const;
    } catch {
      return [bytes, text, null] as const;
    }
  }, [contentBase64, contentType]);

  const language = inferLanguage(contentType, ptr);
  const imageDataUrl = contentType.startsWith("image/")
    ? `data:${contentType};base64,${contentBase64}`
    : "";
  const jsonView = decodedJson !== null;
  const textView = looksLikeText(contentType) || jsonView;
  const binaryView = !textView && !imageDataUrl;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Badge variant={artifactTypeVariant(contentType)}>{contentType || "application/octet-stream"}</Badge>
          <span className="text-xs text-muted">{formatBytes(sizeBytes ?? decodedBytes.byteLength)}</span>
        </div>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => downloadContent(ptr, contentType, decodedBytes)}
        >
          <Download className="h-3.5 w-3.5" />
          Download
        </Button>
      </div>

      {imageDataUrl && (
        <div className="overflow-hidden rounded-xl border border-border/60 bg-white">
          <img
            src={imageDataUrl}
            alt={ptr}
            className="max-h-[28rem] w-full object-contain"
          />
        </div>
      )}

      {jsonView && (
        <div className="rounded-xl border border-border/60 bg-surface2/30 p-3">
          <div className="mb-2 flex items-center gap-2 text-xs font-semibold text-ink">
            <FileJson className="h-3.5 w-3.5" />
            JSON Tree
          </div>
          <JsonNode value={decodedJson} />
        </div>
      )}

      {textView && (
        <div className="overflow-hidden rounded-xl border border-border/60">
          <Editor
            height="320px"
            language={language}
            value={decodedText}
            theme="vs-dark"
            options={{
              readOnly: true,
              minimap: { enabled: false },
              lineNumbers: "on",
              wordWrap: "on",
              scrollBeyondLastLine: false,
              automaticLayout: true,
              fontSize: 12,
            }}
          />
        </div>
      )}

      {binaryView && (
        <div className="rounded-xl border border-border/60 bg-surface2/25 p-4">
          <div className="mb-2 flex items-center gap-2 text-sm font-semibold text-ink">
            <PackageOpen className="h-4 w-4" />
            Binary Artifact
          </div>
          <p className="text-xs text-muted">
            This artifact cannot be rendered inline. Use download to inspect the raw bytes.
          </p>
        </div>
      )}
    </div>
  );
}

export function ArtifactPanel({ jobId }: ArtifactPanelProps) {
  const { data, isLoading, isError, error, refetch } = useJobArtifacts(jobId);
  const [selectedPtr, setSelectedPtr] = useState<string>("");
  const artifactRows = data ?? [];

  useEffect(() => {
    if (artifactRows.length === 0) {
      setSelectedPtr("");
      return;
    }
    if (!selectedPtr || !artifactRows.some((row) => row.ptr === selectedPtr)) {
      setSelectedPtr(artifactRows[0].ptr);
    }
  }, [artifactRows, selectedPtr]);

  const selectedRef: JobArtifactRef | undefined = useMemo(
    () => artifactRows.find((row) => row.ptr === selectedPtr),
    [artifactRows, selectedPtr],
  );
  const {
    data: selectedArtifact,
    isLoading: isLoadingArtifact,
    isError: isArtifactError,
    error: artifactError,
    refetch: refetchArtifact,
  } = useArtifact(selectedPtr || undefined);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Artifacts</CardTitle>
      </CardHeader>

      {isLoading && (
        <div className="space-y-3">
          {Array.from({ length: 3 }).map((_, index) => (
            <div key={index} className="h-12 animate-pulse rounded-2xl bg-surface2" />
          ))}
        </div>
      )}

      {isError && (
        <div className="rounded-2xl border border-danger/40 bg-danger/5 p-4">
          <div className="mb-2 flex items-center gap-2 text-danger">
            <AlertTriangle className="h-4 w-4" />
            <span className="text-sm font-semibold">Failed to load artifacts</span>
          </div>
          <p className="text-xs text-muted">
            {error instanceof Error ? error.message : "Unknown error"}
          </p>
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="mt-3"
            onClick={() => {
              void refetch();
            }}
          >
            <Loader2 className="h-3.5 w-3.5" />
            Retry
          </Button>
        </div>
      )}

      {!isLoading && !isError && artifactRows.length === 0 && (
        <EmptyState
          icon={PackageOpen}
          title="No artifacts produced"
          description="This job does not expose artifact pointers yet."
        />
      )}

      {!isLoading && !isError && artifactRows.length > 0 && (
        <div className="space-y-4">
          <div className="overflow-x-auto rounded-2xl border border-border/60">
            <table className="min-w-full text-sm">
              <thead className="bg-surface2/35 text-left text-xs uppercase tracking-wider text-muted">
                <tr>
                  <th className="px-4 py-2 font-semibold">Pointer</th>
                  <th className="px-4 py-2 font-semibold">Type</th>
                  <th className="px-4 py-2 font-semibold">Size</th>
                  <th className="px-4 py-2 font-semibold">Timestamp</th>
                </tr>
              </thead>
              <tbody>
                {artifactRows.map((artifact) => {
                  const active = artifact.ptr === selectedPtr;
                  const contentType = artifact.contentType || "unknown";
                  return (
                    <tr
                      key={artifact.ptr}
                      className={`cursor-pointer border-t border-border/40 transition ${
                        active ? "bg-accent/10" : "hover:bg-surface2/25"
                      }`}
                      onClick={() => setSelectedPtr(artifact.ptr)}
                    >
                      <td className="px-4 py-2 font-mono text-xs text-ink" title={artifact.ptr}>
                        {truncatePointer(artifact.ptr)}
                      </td>
                      <td className="px-4 py-2 text-xs">
                        <Badge variant={artifactTypeVariant(contentType)}>{contentType}</Badge>
                      </td>
                      <td className="px-4 py-2 text-xs text-muted">{formatBytes(artifact.sizeBytes)}</td>
                      <td className="px-4 py-2 text-xs text-muted">{formatTimestamp(artifact.timestamp)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          {selectedRef && (
            <div className="space-y-2 rounded-2xl border border-border/60 bg-white/30 p-4">
              <div className="flex items-center gap-2 text-xs text-muted">
                {selectedRef.contentType?.includes("json") ? (
                  <FileJson className="h-3.5 w-3.5" />
                ) : selectedRef.contentType?.startsWith("image/") ? (
                  <ImageIcon className="h-3.5 w-3.5" />
                ) : looksLikeText(selectedRef.contentType ?? "") ? (
                  <FileCode className="h-3.5 w-3.5" />
                ) : (
                  <FileSearch className="h-3.5 w-3.5" />
                )}
                <span className="font-mono text-[11px] text-ink" title={selectedRef.ptr}>
                  {selectedRef.ptr}
                </span>
              </div>

              {isLoadingArtifact && (
                <div className="flex items-center gap-2 py-5 text-sm text-muted">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  Loading artifact content...
                </div>
              )}

              {isArtifactError && (
                <div className="rounded-xl border border-danger/40 bg-danger/5 p-4">
                  <p className="text-sm font-semibold text-danger">Failed to load artifact content.</p>
                  <p className="mt-1 text-xs text-muted">
                    {artifactError instanceof Error ? artifactError.message : "Unknown error"}
                  </p>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="mt-3"
                    onClick={() => {
                      void refetchArtifact();
                    }}
                  >
                    <Loader2 className="h-3.5 w-3.5" />
                    Retry
                  </Button>
                </div>
              )}

              {!isLoadingArtifact && !isArtifactError && selectedArtifact && (
                <ArtifactViewer
                  ptr={selectedArtifact.artifact_ptr || selectedRef.ptr}
                  contentType={
                    selectedArtifact.metadata?.content_type ||
                    selectedRef.contentType ||
                    "application/octet-stream"
                  }
                  contentBase64={selectedArtifact.content_base64}
                  sizeBytes={selectedArtifact.metadata?.size_bytes ?? selectedRef.sizeBytes}
                />
              )}
            </div>
          )}
        </div>
      )}
    </Card>
  );
}
