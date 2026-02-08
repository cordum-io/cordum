import { useState } from "react";
import { Download, Loader } from "lucide-react";
import { Button } from "../ui/Button";
import { get } from "../../api/client";
import { downloadFile } from "../../lib/export";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const REDACT_KEYS = /secret|password|token|apikey|api_key|credential/i;

function redactSecrets(obj: unknown): unknown {
  if (obj === null || obj === undefined) return obj;
  if (typeof obj === "string") return obj;
  if (Array.isArray(obj)) return obj.map(redactSecrets);
  if (typeof obj === "object") {
    const result: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(obj as Record<string, unknown>)) {
      if (REDACT_KEYS.test(key) && typeof value === "string") {
        result[key] = "[REDACTED]";
      } else {
        result[key] = redactSecrets(value);
      }
    }
    return result;
  }
  return obj;
}

// ---------------------------------------------------------------------------
// DownloadDiagnosticsButton
// ---------------------------------------------------------------------------

export function DownloadDiagnosticsButton() {
  const [loading, setLoading] = useState(false);

  async function handleDownload() {
    setLoading(true);
    try {
      const [status, config] = await Promise.all([
        get<Record<string, unknown>>("/status").catch(() => ({ error: "Failed to fetch status" })),
        get<Record<string, unknown>>("/config").catch(() => ({ error: "Failed to fetch config" })),
      ]);

      const bundle = {
        collectedAt: new Date().toISOString(),
        browser: navigator.userAgent,
        windowLocation: window.location.origin,
        status,
        config: redactSecrets(config),
      };

      const timestamp = new Date().toISOString().replace(/[:.]/g, "-").slice(0, 19);
      downloadFile(
        JSON.stringify(bundle, null, 2),
        `cordum-diagnostics-${timestamp}.json`,
        "application/json",
      );
    } finally {
      setLoading(false);
    }
  }

  return (
    <Button variant="outline" size="sm" onClick={handleDownload} disabled={loading}>
      {loading ? (
        <><Loader className="mr-1.5 h-3 w-3 animate-spin" /> Collecting...</>
      ) : (
        <><Download className="mr-1.5 h-3 w-3" /> Download Diagnostics</>
      )}
    </Button>
  );
}
