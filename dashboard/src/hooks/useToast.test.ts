import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithQueryClient } from "./__tests__/test-utils";
import { useToast } from "./useToast";

const { addToastMock, dismissToastMock } = vi.hoisted(() => ({
  addToastMock: vi.fn(),
  dismissToastMock: vi.fn(),
}));

vi.mock("../state/toast", () => ({
  useToastStore: (selector: (state: { addToast: typeof addToastMock; dismissToast: typeof dismissToastMock }) => unknown) =>
    selector({ addToast: addToastMock, dismissToast: dismissToastMock }),
}));

describe("useToast", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("success() calls addToast with success type", () => {
    const hook = renderWithQueryClient(() => useToast());

    hook.result.current?.success("Saved", "ok");

    expect(addToastMock).toHaveBeenCalledWith({ type: "success", title: "Saved", description: "ok" });
    hook.unmount();
  });

  it("error() calls addToast with error type and 8000 duration", () => {
    const hook = renderWithQueryClient(() => useToast());

    hook.result.current?.error("Failed", "boom");

    expect(addToastMock).toHaveBeenCalledWith({
      type: "error",
      title: "Failed",
      description: "boom",
      duration: 8000,
    });
    hook.unmount();
  });

  it("warning() calls addToast with warning type", () => {
    const hook = renderWithQueryClient(() => useToast());

    hook.result.current?.warning("Careful", "warn");

    expect(addToastMock).toHaveBeenCalledWith({ type: "warning", title: "Careful", description: "warn" });
    hook.unmount();
  });

  it("info() calls addToast with info type", () => {
    const hook = renderWithQueryClient(() => useToast());

    hook.result.current?.info("Info", "details");

    expect(addToastMock).toHaveBeenCalledWith({ type: "info", title: "Info", description: "details" });
    hook.unmount();
  });

  it("exposes dismissToast", () => {
    const hook = renderWithQueryClient(() => useToast());

    expect(hook.result.current?.dismissToast).toBe(dismissToastMock);
    hook.unmount();
  });
});
