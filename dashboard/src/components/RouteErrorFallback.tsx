import { Bug } from "lucide-react";
import { ErrorBanner } from "./ui/ErrorBanner";

export interface RouteErrorFallbackProps {
  route: string;
  error: Error;
  reset: () => void;
}

const REPORT_BUG_EMAIL = "support@cordum.io";

function buildBugReportMailto(route: string, error: Error): string {
  const subject = encodeURIComponent(`[dashboard] Render error on ${route}`);
  const userAgent = typeof navigator !== "undefined" ? navigator.userAgent : "unknown";
  const body = encodeURIComponent(
    [
      `Route: ${route}`,
      `Error: ${error.message || "(no message)"}`,
      "",
      "Stack:",
      error.stack ?? "(no stack)",
      "",
      `User-Agent: ${userAgent}`,
    ].join("\n"),
  );
  return `mailto:${REPORT_BUG_EMAIL}?subject=${subject}&body=${body}`;
}

export function RouteErrorFallback({ route, error, reset }: RouteErrorFallbackProps) {
  return (
    <div className="flex min-h-[400px] items-center justify-center px-4 py-6">
      <div className="w-full max-w-2xl">
        <ErrorBanner
          title={`Couldn't load ${route}`}
          message={error.message || "An unexpected error occurred while rendering this page."}
          onRetry={reset}
        />
        <div className="mt-2 flex justify-center">
          <a
            href={buildBugReportMailto(route, error)}
            className="inline-flex items-center gap-1 text-xs font-medium text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
          >
            <Bug className="w-3 h-3" aria-hidden="true" />
            Report bug
          </a>
        </div>
      </div>
    </div>
  );
}
