import { create } from "zustand";
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
}

export const useEventStore = create<EventState>((set) => ({
  status: "disconnected",
  setStatus: (status) => set({ status }),

  events: [],
  addEvent: (event) =>
    set((state) => ({
      events: [event, ...state.events].slice(0, MAX_EVENTS),
    })),
  clearEvents: () => set({ events: [] }),

  safetyDecisions: [],
  pushSafetyDecision: (event) =>
    set((state) => ({
      safetyDecisions: [event, ...state.safetyDecisions].slice(0, MAX_SAFETY_EVENTS),
    })),
}));
