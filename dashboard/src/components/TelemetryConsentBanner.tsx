import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BarChart3, X } from "lucide-react";
import { get, post } from "@/api/client";
import { Button } from "@/components/ui/Button";
import { useToastStore } from "@/state/toast";
import { cn } from "@/lib/utils";

const DISMISSED_KEY = "cordum-telemetry-consent-dismissed";

interface TelemetryStatus {
  mode?: string;
}

function isDismissed(): boolean {
  try {
    return localStorage.getItem(DISMISSED_KEY) === "true";
  } catch {
    return false;
  }
}

function persistDismissal(): void {
  try {
    localStorage.setItem(DISMISSED_KEY, "true");
  } catch {
    // localStorage unavailable
  }
}

export function TelemetryConsentBanner() {
  const [dismissed, setDismissed] = useState(isDismissed);
  const queryClient = useQueryClient();

  const telemetryStatus = useQuery<TelemetryStatus>({
    queryKey: ["telemetry", "status"],
    queryFn: () => get<TelemetryStatus>("/telemetry/status"),
    staleTime: 60_000,
    retry: 1,
    enabled: !dismissed,
  });

  const setConsent = useMutation<{ mode: string }, Error, string>({
    mutationFn: (mode: string) =>
      post<{ mode: string }>("/telemetry/consent", { mode }),
    onSuccess: (_, mode) => {
      queryClient.invalidateQueries({ queryKey: ["telemetry"] });
      persistDismissal();
      setDismissed(true);
      const label = mode === "anonymous" ? "enabled" : "kept local";
      useToastStore.getState().addToast({
        type: "success",
        title: `Telemetry ${label}. Thank you.`,
      });
    },
  });

  const dismiss = () => {
    persistDismissal();
    setDismissed(true);
  };

  // Don't show if already dismissed, still loading, or already opted in
  if (dismissed) return null;
  const mode = telemetryStatus.data?.mode;
  if (!mode) return null;
  if (mode === "anonymous") return null; // already opted in

  return (
    <div
      className={cn(
        "relative border-b border-border bg-surface-1 px-4 py-3",
        "animate-in fade-in slide-in-from-top-2 duration-300",
      )}
    >
      <div className="mx-auto flex max-w-7xl items-center gap-4">
        <div className="flex shrink-0 items-center justify-center rounded-full bg-cordum/10 p-2">
          <BarChart3 className="h-4 w-4 text-cordum" />
        </div>

        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium text-foreground">
            Help improve Cordum
          </p>
          <p className="text-xs text-muted-foreground">
            Share anonymous usage data (worker counts, job volume, feature
            adoption). No prompts, secrets, tenant names, or PII — ever.{" "}
            <a
              href="/settings/license"
              className="underline hover:text-foreground"
            >
              See exactly what is collected.
            </a>
          </p>
        </div>

        <div className="flex shrink-0 items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setConsent.mutate("local_only");
            }}
            disabled={setConsent.isPending}
          >
            Decline
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={() => {
              setConsent.mutate("anonymous");
            }}
            disabled={setConsent.isPending}
          >
            Share data
          </Button>
        </div>

        <button
          type="button"
          onClick={dismiss}
          className="shrink-0 rounded-full p-1 text-muted-foreground hover:bg-surface-2 hover:text-foreground"
          aria-label="Dismiss"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  );
}
