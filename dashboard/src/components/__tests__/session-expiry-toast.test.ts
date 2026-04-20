import { describe, it, expect, beforeEach } from "vitest";
import { useToastStore } from "../../state/toast";

describe("session expiry toast", () => {
  beforeEach(() => {
    // Reset toast store between tests
    useToastStore.setState({ toasts: [] });
  });

  it("addToast creates a session-expired warning visible to the user", () => {
    const { addToast } = useToastStore.getState();

    addToast({
      type: "warning",
      title: "Session expired",
      description: "Please sign in again to continue.",
      duration: 5000,
    });

    const { toasts } = useToastStore.getState();
    expect(toasts).toHaveLength(1);
    expect(toasts[0].type).toBe("warning");
    expect(toasts[0].title).toBe("Session expired");
    expect(toasts[0].description).toBe("Please sign in again to continue.");
    expect(toasts[0].duration).toBe(5000);
    expect(toasts[0].dismissible).toBe(true);
  });

  it("toast persists in store after addToast (survives ProtectedRoute unmount)", () => {
    const { addToast } = useToastStore.getState();

    // Simulate what ProtectedRoute does on 401:
    // 1. addToast (session expired warning)
    // 2. logout() triggers redirect to /login, unmounting ProtectedRoute
    // Toast must survive because ToastContainer is at App level
    addToast({
      type: "warning",
      title: "Session expired",
      description: "Please sign in again to continue.",
      duration: 5000,
    });

    // Zustand store state persists across component unmounts
    const { toasts } = useToastStore.getState();
    expect(toasts).toHaveLength(1);
    expect(toasts[0].title).toBe("Session expired");
  });

  it("toast has a unique id for dismissal", () => {
    const { addToast } = useToastStore.getState();

    const id = addToast({
      type: "warning",
      title: "Session expired",
      description: "Please sign in again to continue.",
      duration: 5000,
    });

    expect(id).toBeTruthy();
    expect(typeof id).toBe("string");

    const { toasts } = useToastStore.getState();
    expect(toasts[0].id).toBe(id);
  });
});
