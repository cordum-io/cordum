import { get, post } from "../api/client";
import type { User, Approval, DLQEntry } from "../api/types";
import { mapDLQEntry, mapApprovalItem, type BackendDLQEntry, type BackendApprovalItem } from "../api/transform";

interface SessionResponse {
  user: User;
}

interface ApprovalsResponse {
  items: Approval[];
}

interface DLQResponse {
  items: DLQEntry[];
}

export const api = {
  getSession(): Promise<SessionResponse> {
    return get<SessionResponse>("/auth/session");
  },

  logout(): Promise<void> {
    return post<void>("/auth/logout");
  },

  listApprovals(limit: number): Promise<ApprovalsResponse> {
    return get<{ items: BackendApprovalItem[] }>(`/approvals?limit=${limit}`).then((res) => ({
      items: (res.items ?? [])
        .map(mapApprovalItem)
        .filter((v): v is Approval => !!v),
    }));
  },

  listDLQPage(limit: number): Promise<DLQResponse> {
    return get<{ items: BackendDLQEntry[] }>(`/dlq/page?limit=${limit}`).then((res) => ({
      items: (res.items ?? []).map(mapDLQEntry),
    }));
  },
};
