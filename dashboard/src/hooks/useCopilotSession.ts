import { useQuery } from "@tanstack/react-query";
import { ApiError, get } from "../api/client";
import type { CopilotSessionDetailResponse } from "../api/types";

export function useCopilotSession(sessionId: string | undefined) {
  const trimmed = sessionId?.trim() ?? "";

  return useQuery<CopilotSessionDetailResponse, Error>({
    queryKey: ["copilot-session", trimmed],
    enabled: Boolean(trimmed),
    staleTime: 10_000,
    queryFn: async () => {
      try {
        return await get<CopilotSessionDetailResponse>(
          `/copilot/sessions/${encodeURIComponent(trimmed)}`,
        );
      } catch (error) {
        if (error instanceof ApiError && error.status === 404) {
          throw new Error("session not found");
        }
        throw error;
      }
    },
  });
}