import { useCallback } from "react";

export type PolicyStudioTelemetryEventName =
  | "policy_editor_advanced_toggled"
  | "policy_editor_section_viewed"
  | "policy_editor_saved_with_advanced_fields"
  | "policy_editor_saved_with_hidden_advanced_unviewed";

interface PolicyStudioTelemetryPayload {
  scope: "input_global";
  decision?: string;
  configuredAdvancedCount?: number;
  advancedOpen?: boolean;
  section?: "advanced" | "constraints" | "remediations";
  clearRemediationsOnSave?: boolean;
}

interface PolicyStudioTelemetryEvent {
  event: PolicyStudioTelemetryEventName;
  payload: PolicyStudioTelemetryPayload;
  timestamp: string;
}

function isTelemetryEnabled(): boolean {
  return import.meta.env.VITE_POLICY_STUDIO_TELEMETRY === "true";
}

export function usePolicyStudioTelemetry() {
  const emit = useCallback(
    (
      event: PolicyStudioTelemetryEventName,
      payload: PolicyStudioTelemetryPayload,
    ) => {
      if (!isTelemetryEnabled() || typeof window === "undefined") {
        return;
      }

      const detail: PolicyStudioTelemetryEvent = {
        event,
        payload,
        timestamp: new Date().toISOString(),
      };

      window.dispatchEvent(
        new CustomEvent("cordum:policy-studio-telemetry", { detail }),
      );

      if (import.meta.env.DEV) {
        console.debug("[policy-telemetry]", detail);
      }
    },
    [],
  );

  return { emit, enabled: isTelemetryEnabled() };
}
