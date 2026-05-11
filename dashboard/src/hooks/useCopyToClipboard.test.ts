import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useCopyToClipboard } from "./useCopyToClipboard";

describe("useCopyToClipboard", () => {
  let writeTextSpy: ReturnType<typeof vi.fn>;
  let originalClipboard: PropertyDescriptor | undefined;

  beforeEach(() => {
    vi.useFakeTimers();
    writeTextSpy = vi.fn().mockResolvedValue(undefined);
    originalClipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: writeTextSpy },
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    if (originalClipboard) {
      Object.defineProperty(navigator, "clipboard", originalClipboard);
    } else {
      // @ts-expect-error — test cleanup
      delete navigator.clipboard;
    }
  });

  it("starts with copied=false", () => {
    const { result } = renderHook(() => useCopyToClipboard());
    expect(result.current.copied).toBe(false);
  });

  it("writes to navigator.clipboard.writeText with the given value", async () => {
    const { result } = renderHook(() => useCopyToClipboard());
    await act(async () => {
      await result.current.copy("hello");
    });
    expect(writeTextSpy).toHaveBeenCalledWith("hello");
  });

  it("flips copied -> true on success and back to false after resetMs", async () => {
    const { result } = renderHook(() => useCopyToClipboard({ resetMs: 1500 }));
    await act(async () => {
      await result.current.copy("hi");
    });
    expect(result.current.copied).toBe(true);
    act(() => {
      vi.advanceTimersByTime(1499);
    });
    expect(result.current.copied).toBe(true);
    act(() => {
      vi.advanceTimersByTime(1);
    });
    expect(result.current.copied).toBe(false);
  });

  it("uses 1500ms as default resetMs", async () => {
    const { result } = renderHook(() => useCopyToClipboard());
    await act(async () => {
      await result.current.copy("default");
    });
    expect(result.current.copied).toBe(true);
    act(() => {
      vi.advanceTimersByTime(1500);
    });
    expect(result.current.copied).toBe(false);
  });

  it("does NOT auto-reset when resetMs=0 (caller-managed)", async () => {
    const { result } = renderHook(() => useCopyToClipboard({ resetMs: 0 }));
    await act(async () => {
      await result.current.copy("manual");
    });
    expect(result.current.copied).toBe(true);
    act(() => {
      vi.advanceTimersByTime(60_000);
    });
    expect(result.current.copied).toBe(true);
  });

  it("invokes onSuccess after a successful write", async () => {
    const onSuccess = vi.fn();
    const { result } = renderHook(() => useCopyToClipboard({ onSuccess }));
    await act(async () => {
      await result.current.copy("ok");
    });
    expect(onSuccess).toHaveBeenCalledTimes(1);
  });

  it("calls onError and leaves copied=false when writeText rejects", async () => {
    const failure = new Error("insecure context");
    writeTextSpy.mockRejectedValueOnce(failure);
    const onError = vi.fn();
    const onSuccess = vi.fn();
    const { result } = renderHook(() =>
      useCopyToClipboard({ onError, onSuccess }),
    );
    await act(async () => {
      await result.current.copy("fail");
    });
    // act() awaits the copy() promise which already swallows the rejection
    // internally, so onError has been called by the time act resolves —
    // no need for waitFor (which would hang under fake timers).
    expect(onError).toHaveBeenCalledWith(failure);
    expect(onSuccess).not.toHaveBeenCalled();
    expect(result.current.copied).toBe(false);
  });

  it("never throws on rejection (caller's await resolves)", async () => {
    writeTextSpy.mockRejectedValueOnce(new Error("denied"));
    const { result } = renderHook(() => useCopyToClipboard());
    await expect(
      act(async () => {
        await result.current.copy("safe");
      }),
    ).resolves.toBeUndefined();
  });
});
