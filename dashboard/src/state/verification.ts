import { create } from "zustand";
import { persist } from "zustand/middleware";

// lastVerifiedAt persists the UNIX ms timestamp of the last successful
// /audit/verify response per tenant. The full result lives in React
// Query's cache and is deliberately NOT persisted here — VerifyResult
// payloads can be tens of kilobytes once the gap array fills up, and
// re-fetching is cheap. Only the timestamp survives a page reload so
// the "Last verified at … ago" caption is correct when a compliance
// reviewer returns to the page.
//
// dismissedGapBanners tracks which compromised-state banners the
// current user has dismissed during this browser session (cleared on
// reload via a non-persisted middleware strip).
interface VerificationState {
  lastVerifiedAt: Record<string, number>;
  dismissedGapBanners: Record<string, boolean>;
  setLastVerifiedAt: (tenant: string, at: number) => void;
  dismissGapBanner: (tenant: string) => void;
  resetGapBannerDismissal: (tenant: string) => void;
}

export const VERIFICATION_STORAGE_KEY = "cordum-verification-state";

export const useVerificationStore = create<VerificationState>()(
  persist(
    (set) => ({
      lastVerifiedAt: {},
      dismissedGapBanners: {},
      setLastVerifiedAt: (tenant, at) =>
        set((s) => ({
          lastVerifiedAt: { ...s.lastVerifiedAt, [tenant]: at },
        })),
      dismissGapBanner: (tenant) =>
        set((s) => ({
          dismissedGapBanners: { ...s.dismissedGapBanners, [tenant]: true },
        })),
      resetGapBannerDismissal: (tenant) =>
        set((s) => {
          const next = { ...s.dismissedGapBanners };
          delete next[tenant];
          return { dismissedGapBanners: next };
        }),
    }),
    {
      name: VERIFICATION_STORAGE_KEY,
      // Only persist the timestamp; banner dismissal is per-session
      // by design so a compromised-state alert re-appears on next
      // login until the underlying issue is resolved.
      partialize: (s) => ({ lastVerifiedAt: s.lastVerifiedAt }),
    },
  ),
);

// clearVerificationState is intended to be called from the auth flow
// whenever the principalId changes (login, logout, tenant-lock
// change). Keeps the persisted timestamp user-scoped: if operator A
// logs out and operator B logs in on the same browser, B starts
// with a clean "never verified" caption instead of inheriting A's
// timestamp. The setter path handles both the in-memory store and
// the underlying localStorage entry.
export function clearVerificationState(): void {
  useVerificationStore.setState({
    lastVerifiedAt: {},
    dismissedGapBanners: {},
  });
  if (typeof window !== "undefined") {
    try {
      window.localStorage.removeItem(VERIFICATION_STORAGE_KEY);
    } catch {
      // Private-mode browsers may throw here; ignore.
    }
  }
}
