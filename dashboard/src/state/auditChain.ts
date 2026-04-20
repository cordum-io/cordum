import { create } from "zustand";

// Per-tenant UI state for the AuditChainCard. Expanded state is
// tracked so a user who opens the gap list on one page doesn't have
// it collapse back when they navigate to another page that also
// mounts the card (Command Center ↔ Governance Overview).
interface AuditChainUIState {
  expandedByTenant: Record<string, boolean>;
  toggleExpanded: (tenant: string) => void;
  setExpanded: (tenant: string, value: boolean) => void;
  isExpanded: (tenant: string) => boolean;
}

export const useAuditChainUI = create<AuditChainUIState>()((set, get) => ({
  expandedByTenant: {},
  toggleExpanded: (tenant) =>
    set((s) => ({
      expandedByTenant: {
        ...s.expandedByTenant,
        [tenant]: !s.expandedByTenant[tenant],
      },
    })),
  setExpanded: (tenant, value) =>
    set((s) => ({
      expandedByTenant: { ...s.expandedByTenant, [tenant]: value },
    })),
  isExpanded: (tenant) => Boolean(get().expandedByTenant[tenant]),
}));
