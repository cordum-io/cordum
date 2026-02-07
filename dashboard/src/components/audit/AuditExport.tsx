import { useState, useCallback, useRef, useEffect } from "react";
import { Download, ChevronDown, Loader } from "lucide-react";
import { Button } from "../ui/Button";
import { get } from "../../api/client";
import { toCsv, downloadFile } from "../../lib/export";
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
  // No page/perPage — fetch all filtered results
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

const AUDIT_CSV_HEADERS = [
  "Timestamp",
  "Event Type",
  "Actor",
  "Resource Type",
  "Resource ID",
  "Action",
  "Message",
];

function entriesToRows(entries: AuditEntry[]): string[][] {
  return entries.map((e) => [
    e.timestamp,
    e.eventType,
    e.actor,
    e.resourceType,
    e.resourceId,
    e.action,
    e.message,
  ]);
}

// ---------------------------------------------------------------------------
// AuditExport
// ---------------------------------------------------------------------------

export function AuditExport({ filters }: { filters: AuditFilters }) {
  const [open, setOpen] = useState(false);
  const [exporting, setExporting] = useState(false);
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

  const handleExport = useCallback(
    async (format: ExportFormat) => {
      setOpen(false);
      setExporting(true);
      try {
        const resp = await get<{ items: BackendPolicyAuditEntry[] }>(
          `/policy/audit${buildExportParams(filters)}`,
        );
        const entries = (resp.items ?? []).map(mapPolicyAuditEntry);

        if (format === "csv") {
          const csv = toCsv(AUDIT_CSV_HEADERS, entriesToRows(entries));
          downloadFile(csv, "audit-log.csv", "text/csv;charset=utf-8");
        } else {
          const json = JSON.stringify(entries, null, 2);
          downloadFile(json, "audit-log.json", "application/json");
        }
      } catch {
        // Silently fail — user can retry
      } finally {
        setExporting(false);
      }
    },
    [filters],
  );

  return (
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
        <div className="absolute right-0 z-20 mt-1 w-36 overflow-hidden rounded-xl border border-border bg-white shadow-lg">
          <button
            type="button"
            className="flex w-full items-center gap-2 px-4 py-2.5 text-sm text-ink transition hover:bg-surface2/60"
            onClick={() => handleExport("csv")}
          >
            CSV
          </button>
          <button
            type="button"
            className="flex w-full items-center gap-2 px-4 py-2.5 text-sm text-ink transition hover:bg-surface2/60"
            onClick={() => handleExport("json")}
          >
            JSON
          </button>
        </div>
      )}
    </div>
  );
}
