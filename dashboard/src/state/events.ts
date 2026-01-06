import { create } from "zustand";

export type LiveEvent = {
  id: string;
  timestamp: string;
  title: string;
  detail?: string;
  severity: "info" | "warning" | "danger" | "success";
  source?: string;
  jobId?: string;
  runId?: string;
  workflowId?: string;
  eventType?: string;
};

type EventState = {
  events: LiveEvent[];
  status: "connected" | "connecting" | "disconnected";
  addEvent: (event: LiveEvent) => void;
  setStatus: (status: EventState["status"]) => void;
  clear: () => void;
};

const MAX_EVENTS = 120;

export const useEventStore = create<EventState>((set) => ({
  events: [],
  status: "disconnected",
  addEvent: (event) =>
    set((state) => ({
      events: [event, ...state.events].slice(0, MAX_EVENTS),
    })),
  setStatus: (status) => set({ status }),
  clear: () => set({ events: [] }),
}));
