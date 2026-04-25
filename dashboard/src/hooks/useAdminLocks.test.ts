import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithQueryClient } from "./__tests__/test-utils";
import { useAdminLocks } from "./useAdminLocks";

const { isAdminState, getMock } = vi.hoisted(() => ({
  isAdminState: {
    value: false,
  },
  getMock: vi.fn(),
}));

vi.mock("../api/client", () => ({
  get: getMock,
}));

vi.mock("./usePermission", () => ({
  useIsAdmin: () => isAdminState.value,
}));

describe("useAdminLocks", () => {
  beforeEach(() => {
    isAdminState.value = false;
    getMock.mockReset();
    getMock.mockResolvedValue({ locks: [] });
  });

  it("does not request /admin/locks for non-admin users", async () => {
    const hook = renderWithQueryClient(() => useAdminLocks());

    await hook.waitFor(() => {
      expect(hook.result.current?.isFetching).toBe(false);
    });

    expect(hook.result.current?.data).toBeUndefined();
    expect(hook.result.current?.isAdmin).toBe(false);
    expect(hook.result.current?.isLoading).toBe(false);
    expect(hook.result.current?.isError).toBe(false);
    expect(getMock).not.toHaveBeenCalled();

    hook.unmount();
  });

  it("requests /admin/locks for admin users", async () => {
    isAdminState.value = true;

    const hook = renderWithQueryClient(() => useAdminLocks());

    await hook.waitFor(() => {
      expect(getMock).toHaveBeenCalledWith("/admin/locks");
      expect(hook.result.current?.data).toEqual({ locks: [] });
    });

    expect(hook.result.current?.isAdmin).toBe(true);

    hook.unmount();
  });
});
