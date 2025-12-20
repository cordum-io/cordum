import { create } from "zustand";

export type AuthStatus = "unknown" | "authorized" | "missing_api_key" | "invalid_api_key";

type AuthState = {
  status: AuthStatus;
  markUnauthorized: (opts: { hasKey: boolean }) => void;
  markAuthorized: () => void;
  reset: () => void;
};

export const useAuthStore = create<AuthState>((set) => ({
  status: "unknown",
  markUnauthorized: ({ hasKey }) => set({ status: hasKey ? "invalid_api_key" : "missing_api_key" }),
  markAuthorized: () => set({ status: "authorized" }),
  reset: () => set({ status: "unknown" }),
}));

