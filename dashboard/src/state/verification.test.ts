import { describe, it, expect, beforeEach } from "vitest";
import {
  clearVerificationState,
  useVerificationStore,
  VERIFICATION_STORAGE_KEY,
} from "./verification";

describe("useVerificationStore", () => {
  beforeEach(() => {
    // Reset to defaults + clear the persisted slice so each test runs
    // against a clean store.
    useVerificationStore.setState({
      lastVerifiedAt: {},
      dismissedGapBanners: {},
    });
    if (typeof window !== "undefined") {
      window.localStorage.removeItem("cordum-verification-state");
    }
  });

  it("records the last-verified timestamp per tenant", () => {
    const { setLastVerifiedAt } = useVerificationStore.getState();
    setLastVerifiedAt("tenant-a", 1_700_000_000_000);
    expect(useVerificationStore.getState().lastVerifiedAt["tenant-a"]).toBe(
      1_700_000_000_000,
    );
  });

  it("keeps separate timestamps for separate tenants", () => {
    const { setLastVerifiedAt } = useVerificationStore.getState();
    setLastVerifiedAt("a", 100);
    setLastVerifiedAt("b", 200);
    const state = useVerificationStore.getState();
    expect(state.lastVerifiedAt["a"]).toBe(100);
    expect(state.lastVerifiedAt["b"]).toBe(200);
  });

  it("dismisses and resets per-tenant banners in memory (not persisted)", () => {
    const s = useVerificationStore.getState();
    s.dismissGapBanner("tenant-a");
    expect(useVerificationStore.getState().dismissedGapBanners["tenant-a"]).toBe(true);

    s.resetGapBannerDismissal("tenant-a");
    expect(useVerificationStore.getState().dismissedGapBanners["tenant-a"]).toBeUndefined();
  });

  it("persists only the timestamp slice, never the banner dismissals", () => {
    const { setLastVerifiedAt, dismissGapBanner } = useVerificationStore.getState();
    setLastVerifiedAt("persisted", 42);
    dismissGapBanner("persisted");

    const raw = window.localStorage.getItem(VERIFICATION_STORAGE_KEY);
    expect(raw).not.toBeNull();
    const parsed = JSON.parse(raw!);
    expect(parsed.state.lastVerifiedAt).toEqual({ persisted: 42 });
    // dismissedGapBanners is partialized out — a compromised-state
    // alert must re-appear on next login until the issue is
    // resolved, so it's deliberately session-only.
    expect(parsed.state.dismissedGapBanners).toBeUndefined();
  });

  it("exposes the localStorage key constant so consumers can build migration paths consistently", () => {
    expect(VERIFICATION_STORAGE_KEY).toBe("cordum-verification-state");
  });

  it("re-hydrates lastVerifiedAt from localStorage on rehydrate()", async () => {
    window.localStorage.setItem(
      VERIFICATION_STORAGE_KEY,
      JSON.stringify({
        state: { lastVerifiedAt: { tenantX: 999 } },
        version: 0,
      }),
    );
    // persist.rehydrate() re-reads from storage; it's the post-login
    // / cross-tab-sync hook point the middleware exposes.
    await useVerificationStore.persist.rehydrate();
    expect(useVerificationStore.getState().lastVerifiedAt["tenantX"]).toBe(999);
  });

  it("skips rehydrate gracefully when the persisted blob is malformed JSON", async () => {
    window.localStorage.setItem(VERIFICATION_STORAGE_KEY, "not-json");
    // Malformed entries must not blow up the app. The store falls
    // back to its initial empty state.
    await useVerificationStore.persist.rehydrate();
    expect(useVerificationStore.getState().lastVerifiedAt).toEqual({});
  });

  it("clearVerificationState() drops both in-memory and localStorage state", () => {
    useVerificationStore.setState({
      lastVerifiedAt: { a: 1, b: 2 },
      dismissedGapBanners: { a: true },
    });
    window.localStorage.setItem(VERIFICATION_STORAGE_KEY, "{}");

    clearVerificationState();

    expect(useVerificationStore.getState().lastVerifiedAt).toEqual({});
    expect(useVerificationStore.getState().dismissedGapBanners).toEqual({});
    expect(window.localStorage.getItem(VERIFICATION_STORAGE_KEY)).toBeNull();
  });
});
