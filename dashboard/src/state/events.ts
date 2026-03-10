import { create } from "zustand";
import { logger } from "../lib/logger";
import type { StreamEvent } from "../api/types";

export type LiveEvent = StreamEvent;

export type WsStatus = "connected" | "connecting" | "disconnected" | "reconnecting";

// ---------------------------------------------------------------------------
// Safety decision events (pushed from WebSocket)
// ---------------------------------------------------------------------------

export interface SafetyDecisionEvent {
  id: string;
  timestamp: string;
  topic: string;
  decision: "allow" | "deny" | "require_approval" | "allow_with_constraints" | "throttle";
  matchedRule?: string;
  evalTimeMs?: number;
}

const MAX_SAFETY_EVENTS = 100;
const MAX_EVENTS = 100;

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

  // Approval presence & assignments (real-time collaboration)
  approvalPresence: Map<string, string>;
  approvalAssignments: Map<string, string>;
  setReviewing: (approvalId: string, user: string) => void;
  clearReviewing: (approvalId: string) => void;
  assignApproval: (approvalId: string, user: string) => void;
  unassignApproval: (approvalId: string) => void;

  // Reset all state (called on logout / tenant switch)
  reset: () => void;
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

  approvalPresence: new Map(),
  approvalAssignments: new Map(),
  setReviewing: (approvalId, user) =>
    set((state) => {
      const next = new Map(state.approvalPresence);
      next.set(approvalId, user);
      return { approvalPresence: next };
    }),
  clearReviewing: (approvalId) =>
    set((state) => {
      const next = new Map(state.approvalPresence);
      next.delete(approvalId);
      return { approvalPresence: next };
    }),
  assignApproval: (approvalId, user) =>
    set((state) => {
      const next = new Map(state.approvalAssignments);
      next.set(approvalId, user);
      return { approvalAssignments: next };
    }),
  unassignApproval: (approvalId) =>
    set((state) => {
      const next = new Map(state.approvalAssignments);
      next.delete(approvalId);
      return { approvalAssignments: next };
    }),

  reset: () =>
    set({
      events: [],
      safetyDecisions: [],
      status: "disconnected",
      approvalPresence: new Map(),
      approvalAssignments: new Map(),
    }),
}));
