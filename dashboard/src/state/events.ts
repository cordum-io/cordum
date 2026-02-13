import { useEffect } from "react";
import { create } from "zustand";
import { logger } from "../lib/logger";
import type { StreamEvent } from "../api/types";

export type WsStatus = "connected" | "connecting" | "disconnected" | "reconnecting";

// ---------------------------------------------------------------------------
// Safety decision events (pushed from WebSocket)
// ---------------------------------------------------------------------------

export interface SafetyDecisionEvent {
  id: string;
  timestamp: string;
  topic: string;
  decision: "allow" | "deny" | "require_approval" | "throttle";
  matchedRule?: string;
  evalTimeMs?: number;
}

const MAX_SAFETY_EVENTS = 100;
const MAX_EVENTS = 100;
const PRESENCE_EXPIRE_MS = 60_000; // 60 seconds

// ---------------------------------------------------------------------------
// Presence & Assignment types
// ---------------------------------------------------------------------------

export interface PresenceEntry {
  actor: string;
  since: number;
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

interface EventState {
  status: WsStatus;
  setStatus: (status: WsStatus) => void;

  // Generic event buffer (last 100) for live feed
  events: StreamEvent[];
  addEvent: (event: StreamEvent) => void;
  clearEvents: () => void;

  // Safety-specific buffer
  safetyDecisions: SafetyDecisionEvent[];
  pushSafetyDecision: (event: SafetyDecisionEvent) => void;

  // Approval presence tracking (who is reviewing which item)
  approvalPresence: Map<string, PresenceEntry>;
  setReviewing: (approvalId: string, actor: string) => void;
  clearReviewing: (approvalId: string) => void;

  // Approval assignment (who is assigned to which item)
  approvalAssignments: Map<string, string>;
  assignApproval: (approvalId: string, actor: string) => void;
  unassignApproval: (approvalId: string) => void;
}

export const useEventStore = create<EventState>((set, get) => ({
  status: "disconnected",
  setStatus: (status) => {
    const prev = get().status;
    if (prev !== status) {
      logger.info("event-store", "WS status changed", { from: prev, to: status });
    }
    set({ status });
  },

  events: [],
  addEvent: (event) =>
    set((state) => {
      const next = [event, ...state.events];
      if (next.length > MAX_EVENTS) {
        logger.debug("event-store", "Event buffer full, dropping oldest");
      }
      return { events: next.slice(0, MAX_EVENTS) };
    }),
  clearEvents: () => set({ events: [] }),

  safetyDecisions: [],
  pushSafetyDecision: (event) =>
    set((state) => ({
      safetyDecisions: [event, ...state.safetyDecisions].slice(0, MAX_SAFETY_EVENTS),
    })),

  // Presence tracking
  approvalPresence: new Map(),
  setReviewing: (approvalId, actor) =>
    set((state) => {
      const next = new Map(state.approvalPresence);
      next.set(approvalId, { actor, since: Date.now() });
      return { approvalPresence: next };
    }),
  clearReviewing: (approvalId) =>
    set((state) => {
      const next = new Map(state.approvalPresence);
      next.delete(approvalId);
      return { approvalPresence: next };
    }),

  // Assignment tracking
  approvalAssignments: new Map(),
  assignApproval: (approvalId, actor) =>
    set((state) => {
      const next = new Map(state.approvalAssignments);
      next.set(approvalId, actor);
      return { approvalAssignments: next };
    }),
  unassignApproval: (approvalId) =>
    set((state) => {
      const next = new Map(state.approvalAssignments);
      next.delete(approvalId);
      return { approvalAssignments: next };
    }),
}));

// ---------------------------------------------------------------------------
// Presence cleanup hook — expire entries older than 60s
// ---------------------------------------------------------------------------

export function usePresenceCleanup(): void {
  useEffect(() => {
    const id = window.setInterval(() => {
      const state = useEventStore.getState();
      const now = Date.now();
      let changed = false;
      const next = new Map(state.approvalPresence);
      for (const [entryId, entry] of next) {
        if (now - entry.since > PRESENCE_EXPIRE_MS) {
          next.delete(entryId);
          changed = true;
        }
      }
      if (changed) {
        useEventStore.setState({ approvalPresence: next });
      }
    }, 15_000);
    return () => window.clearInterval(id);
  }, []);
}
