import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useToastStore } from "./toast";

describe("useToastStore", () => {
  let uuidCounter = 0;
  let randomUUIDSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    useToastStore.setState({ toasts: [] });
    uuidCounter = 0;
    randomUUIDSpy = vi
      .spyOn(globalThis.crypto, "randomUUID")
      .mockImplementation(
        () => `uuid-${++uuidCounter}` as `${string}-${string}-${string}-${string}-${string}`,
      );
  });

  it("addToast creates a toast with defaults and returns generated id", () => {
    const id = useToastStore.getState().addToast({
      type: "success",
      title: "Saved",
      description: "Settings saved",
    });

    expect(id).toBe("uuid-1");
    expect(useToastStore.getState().toasts).toEqual([
      {
        id: "uuid-1",
        type: "success",
        title: "Saved",
        description: "Settings saved",
        duration: 5000,
        dismissible: true,
      },
    ]);
  });

  it("addToast applies custom duration and dismissible overrides", () => {
    useToastStore.getState().addToast({
      type: "warning",
      title: "Long-running task",
      duration: 15_000,
      dismissible: false,
    });

    expect(useToastStore.getState().toasts[0]).toMatchObject({
      id: "uuid-1",
      type: "warning",
      title: "Long-running task",
      duration: 15_000,
      dismissible: false,
    });
  });

  it("prepends new toasts first (newest first ordering)", () => {
    useToastStore.getState().addToast({ type: "info", title: "First" });
    useToastStore.getState().addToast({ type: "info", title: "Second" });

    expect(useToastStore.getState().toasts.map((t) => t.id)).toEqual([
      "uuid-2",
      "uuid-1",
    ]);
  });

  it("enforces MAX_TOASTS=5 by dropping the oldest when a sixth toast is added", () => {
    for (let i = 1; i <= 6; i++) {
      useToastStore.getState().addToast({
        type: "info",
        title: `Toast ${i}`,
      });
    }

    const ids = useToastStore.getState().toasts.map((t) => t.id);
    expect(ids).toHaveLength(5);
    expect(ids).toEqual(["uuid-6", "uuid-5", "uuid-4", "uuid-3", "uuid-2"]);
    expect(ids).not.toContain("uuid-1");
  });

  it("dismissToast removes only the requested toast id", () => {
    useToastStore.getState().addToast({ type: "info", title: "A" });
    useToastStore.getState().addToast({ type: "info", title: "B" });
    useToastStore.getState().addToast({ type: "info", title: "C" });

    useToastStore.getState().dismissToast("uuid-2");
    expect(useToastStore.getState().toasts.map((t) => t.id)).toEqual([
      "uuid-3",
      "uuid-1",
    ]);
  });

  it("dismissToast is a no-op for unknown ids", () => {
    useToastStore.getState().addToast({ type: "error", title: "Boom" });
    const before = useToastStore.getState().toasts;

    useToastStore.getState().dismissToast("missing-id");
    expect(useToastStore.getState().toasts).toEqual(before);
  });

  afterEach(() => {
    randomUUIDSpy.mockRestore();
  });
});
