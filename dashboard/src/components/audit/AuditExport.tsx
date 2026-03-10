import { useState, useCallback, useRef, useEffect } from "react";
import { Download, ChevronDown, Loader, X } from "lucide-react";
import { Button } from "../ui/Button";
import { get } from "../../api/client";
import { toCsv, downloadFile } from "../../lib/export";
import { generateAuditReport } from "../../lib/audit-report";
import type { AuditFilters } from "../../hooks/useAudit";
import type { AuditEntry } from "../../api/types";
import { mapPolicyAuditEntry, type BackendPolicyAuditEntry } from "../../api/transform";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type ExportFormat = "csv" | "json";

function buildExportParams(filters: AuditFilters): string {
  const params = new URLSearchParams();
  if (filters.eventType?.length) {
    params.set("eventType", filters.eventType.join(","));
  }
  if (filters.actor) params.set("actor", filters.actor);
  if (filters.resourceType) params.set("resourceType", filters.resourceType);
  if (filters.timeRange) params.set("timeRange", filters.timeRange);
  if (filters.search) params.set("q", filters.search);
  if (filters.sort) params.set("sort", filters.sort);
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

// ---------------------------------------------------------------------------
// Filename builder
// ---------------------------------------------------------------------------

function buildExportFilename(
  filters: AuditFilters,
  entries: AuditEntry[],
  format: ExportFormat,
): string {
  const parts = ["cordum-audit"];

  // Filter summary
  const filterParts: string[] = [];
  if (filters.eventType?.length) {
    filterParts.push(filters.eventType.join("-"));
  }
  if (filters.resourceType) filterParts.push(filters.resourceType);
  if (filters.actor) filterParts.push(filters.actor);
  if (filterParts.length > 0) {
    parts.push(filterParts.join("_").slice(0, 40));
  }

  // Date range from actual data
  if (entries.length > 0) {
    const timestamps = entries.map((e) => e.timestamp).sort();
    const from = timestamps[0].slice(0, 10);
    const to = timestamps[timestamps.length - 1].slice(0, 10);
    if (from === to) {
      parts.push(from);
    } else {
      parts.push(`${from}_to_${to}`);
    }
  } else {
    parts.push(new Date().toISOString().slice(0, 10));
  }

  return `${parts.join("_")}.${format}`;
}

// ---------------------------------------------------------------------------
// Enhanced CSV columns
// ---------------------------------------------------------------------------

const AUDIT_CSV_HEADERS = [
  "Timestamp",
  "Category",
  "Severity",
  "Event Type",
  "Actor",
  "Actor Type",
  "Resource Type",
  "Resource ID",
  "Action",
  "Message",
  "Payload",
];

function entriesToRows(entries: AuditEntry[]): string[][] {
  return entries.map((e) => [
    e.timestamp,
    e.category ?? "",
    e.severity ?? "",
    e.eventType,
    e.actor,
    e.actorInfo?.type ?? "",
    e.resourceType,
    e.resourceId,
    e.action,
    e.message,
    e.payload ? JSON.stringify(e.payload) : "",
  ]);
}

// ---------------------------------------------------------------------------
// Full JSON export structure
// ---------------------------------------------------------------------------

function entriesToFullJson(entries: AuditEntry[]): string {
  const data = entries.map((e) => ({
    id: e.id,
    timestamp: e.timestamp,
    category: e.category ?? null,
    severity: e.severity ?? null,
    eventType: e.eventType,
    action: e.action,
    message: e.message,
    actor: {
      id: e.actorInfo?.id ?? e.actor,
      name: e.actorInfo?.name ?? e.actor,
      type: e.actorInfo?.type ?? null,
      role: e.actorInfo?.role ?? null,
    },
    resource: {
      type: e.resourceType,
      id: e.resourceId,
      link: e.resourceInfo?.link ?? null,
    },
    payload: e.payload ?? null,
    snapshotBefore: e.snapshotBefore ?? null,
    snapshotAfter: e.snapshotAfter ?? null,
    bundleIds: e.bundleIds ?? [],
  }));
  return JSON.stringify(data, null, 2);
}

// ---------------------------------------------------------------------------
// Chunked export (keeps UI responsive for large datasets)
// ---------------------------------------------------------------------------

interface ExportProgress {
  total: number;
  processed: number;
  cancelled: boolean;
}

async function chunkedExport(
  entries: AuditEntry[],
  format: ExportFormat,
  progress: ExportProgress,
  onProgress: (processed: number) => void,
): Promise<string | null> {
  const CHUNK = 50;

  if (format === "json") {
    // Build full JSON — process in chunks for progress updates
    const results: AuditEntry[] = [];
    for (let i = 0; i < entries.length; i += CHUNK) {
      if (progress.cancelled) return null;
      const chunk = entries.slice(i, i + CHUNK);
      results.push(...chunk);
      onProgress(Math.min(results.length, entries.length));
      // Yield to UI
      await new Promise((r) => setTimeout(r, 0));
    }
    if (progress.cancelled) return null;
    return entriesToFullJson(results);
  }

  // CSV — build rows in chunks
  const allRows: string[][] = [];
  for (let i = 0; i < entries.length; i += CHUNK) {
    if (progress.cancelled) return null;
    const chunk = entries.slice(i, i + CHUNK);
    allRows.push(...entriesToRows(chunk));
    onProgress(Math.min(allRows.length, entries.length));
    await new Promise((r) => setTimeout(r, 0));
  }
  if (progress.cancelled) return null;
  return toCsv(AUDIT_CSV_HEADERS, allRows);
}

// ---------------------------------------------------------------------------
// Progress Modal
// ---------------------------------------------------------------------------

function ExportProgressModal({
  total,
  processed,
  onCancel,
}: {
  total: number;
  processed: number;
  onCancel: () => void;
}) {
  const pct = total > 0 ? Math.round((processed / total) * 100) : 0;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30">
      <div className="w-80 rounded-2xl border border-border bg-surface p-6 shadow-xl space-y-4">
        <div className="flex items-center justify-between">
          <p className="text-sm font-semibold text-ink">Exporting…</p>
          <button
            type="button"
            className="rounded p-1 text-muted-foreground hover:text-ink transition"
            onClick={onCancel}
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="h-2 w-full overflow-hidden rounded-full bg-surface2">
          <div
            className="h-full rounded-full bg-accent transition-all duration-150"
            style={{ width: `${pct}%` }}
          />
        </div>
        <p className="text-xs text-muted-foreground">
          {processed} of {total} events ({pct}%)
        </p>
        <Button variant="outline" size="sm" className="w-full" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// AuditExport
// ---------------------------------------------------------------------------

export function AuditExport({ filters }: { filters: AuditFilters }) {
  const [open, setOpen] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [showProgress, setShowProgress] = useState(false);
  const [progressTotal, setProgressTotal] = useState(0);
  const [progressProcessed, setProgressProcessed] = useState(0);
  const progressRef = useRef<ExportProgress>({ total: 0, processed: 0, cancelled: false });
  const menuRef = useRef<HTMLDivElement>(null);

  // Close dropdown on outside click
  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  const handleCancel = useCallback(() => {
    progressRef.current.cancelled = true;
  }, []);

  const handlePdfReport = useCallback(async () => {
    setOpen(false);
    setExporting(true);
    try {
      const resp = await get<{ items: BackendPolicyAuditEntry[] }>(
        `/policy/audit${buildExportParams(filters)}`,
      );
      const entries = (resp.items ?? []).map(mapPolicyAuditEntry);

      if (entries.length > 1000) {
        const confirmed = window.confirm(
          `This report contains ${entries.length} events. Generate full report or cancel and apply filters to reduce scope?`,
        );
        if (!confirmed) return;
      }

      setProgressTotal(entries.length);
      setProgressProcessed(0);
      setShowProgress(true);

      await generateAuditReport(entries, filters, "System", (n) => {
        setProgressProcessed(n);
      });
    } catch {
      // Silently fail — user can retry
    } finally {
      setExporting(false);
      setShowProgress(false);
    }
  }, [filters]);

  const handleExport = useCallback(
    async (format: ExportFormat) => {
      setOpen(false);
      setExporting(true);
      try {
        const resp = await get<{ items: BackendPolicyAuditEntry[] }>(
          `/policy/audit${buildExportParams(filters)}`,
        );
        const entries = (resp.items ?? []).map(mapPolicyAuditEntry);

        const needsProgress = entries.length >= 100;
        const progress: ExportProgress = {
          total: entries.length,
          processed: 0,
          cancelled: false,
        };
        progressRef.current = progress;

        if (needsProgress) {
          setProgressTotal(entries.length);
          setProgressProcessed(0);
          setShowProgress(true);
        }

        const content = needsProgress
          ? await chunkedExport(entries, format, progress, (n) => {
              setProgressProcessed(n);
            })
          : format === "csv"
            ? toCsv(AUDIT_CSV_HEADERS, entriesToRows(entries))
            : entriesToFullJson(entries);

        if (content != null) {
          const filename = buildExportFilename(filters, entries, format);
          const mime = format === "csv" ? "text/csv;charset=utf-8" : "application/json";
          downloadFile(content, filename, mime);
        }
      } catch {
        // Silently fail — user can retry
      } finally {
        setExporting(false);
        setShowProgress(false);
      }
    },
    [filters],
  );

  return (
    <>
      <div className="relative" ref={menuRef}>
        <Button
          variant="outline"
          size="sm"
          onClick={() => setOpen((v) => !v)}
          disabled={exporting}
        >
          {exporting ? (
            <Loader className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <Download className="h-3.5 w-3.5" />
          )}
          {exporting ? "Exporting\u2026" : "Export"}
          <ChevronDown className="h-3 w-3" />
        </Button>

        {open && (
          <div className="absolute right-0 z-20 mt-1 w-44 overflow-hidden rounded-xl border border-border bg-card shadow-lg">
            <button
              type="button"
              className="flex w-full items-center gap-2 px-4 py-2.5 text-sm text-ink transition hover:bg-surface2/60"
              onClick={() => handleExport("csv")}
            >
              Export as CSV
            </button>
            <button
              type="button"
              className="flex w-full items-center gap-2 px-4 py-2.5 text-sm text-ink transition hover:bg-surface2/60"
              onClick={() => handleExport("json")}
            >
              Export as JSON
            </button>
            <hr className="border-border" />
            <button
              type="button"
              className="flex w-full items-center gap-2 px-4 py-2.5 text-sm text-ink transition hover:bg-surface2/60"
              onClick={() => handlePdfReport()}
            >
              PDF Report
            </button>
          </div>
        )}
      </div>

      {showProgress && (
        <ExportProgressModal
          total={progressTotal}
          processed={progressProcessed}
          onCancel={handleCancel}
        />
      )}
    </>
  );
}
