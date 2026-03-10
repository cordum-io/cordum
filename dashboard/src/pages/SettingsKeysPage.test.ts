import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  handleCreateKeyError,
  handleCreateKeySuccess,
  handleDeleteKeyError,
  handleDeleteKeySuccess,
} from "./SettingsKeysPage";

const toastError = vi.fn();
const toastSuccess = vi.fn();

vi.mock("sonner", () => ({
  toast: {
    error: (...args: unknown[]) => toastError(...args),
    success: (...args: unknown[]) => toastSuccess(...args),
  },
}));

describe("SettingsKeysPage mutation handlers", () => {
  beforeEach(() => {
    toastError.mockReset();
    toastSuccess.mockReset();
  });

  it("handles create success and invalidates list", () => {
    const queryClient = { invalidateQueries: vi.fn() };
    const setCreatedKey = vi.fn();
    const setNewKeyName = vi.fn();

    handleCreateKeySuccess(
      { data: { key: "cordum_live_123" } },
      { queryClient, setCreatedKey, setNewKeyName },
    );

    expect(queryClient.invalidateQueries).toHaveBeenCalledWith({ queryKey: ["api-keys"] });
    expect(setCreatedKey).toHaveBeenCalledWith("cordum_live_123");
    expect(setNewKeyName).toHaveBeenCalledWith("");
    expect(toastError).not.toHaveBeenCalled();
  });

  it("shows create error toast when key payload is missing", () => {
    const queryClient = { invalidateQueries: vi.fn() };
    const setCreatedKey = vi.fn();
    const setNewKeyName = vi.fn();

    handleCreateKeySuccess({ data: {} }, { queryClient, setCreatedKey, setNewKeyName });

    expect(toastError).toHaveBeenCalledWith("API key created but key value not returned");
    expect(setCreatedKey).not.toHaveBeenCalled();
    expect(setNewKeyName).toHaveBeenCalledWith("");
  });

  it("shows create mutation error feedback", () => {
    handleCreateKeyError(new Error("network down"));
    expect(toastError).toHaveBeenCalledWith("Failed to create API key", { description: "network down" });
  });

  it("handles delete success with user feedback and state reset", () => {
    const queryClient = { invalidateQueries: vi.fn() };
    const setDeleteTarget = vi.fn();

    handleDeleteKeySuccess({ queryClient, setDeleteTarget });

    expect(queryClient.invalidateQueries).toHaveBeenCalledWith({ queryKey: ["api-keys"] });
    expect(toastSuccess).toHaveBeenCalledWith("API key revoked");
    expect(setDeleteTarget).toHaveBeenCalledWith(null);
  });

  it("shows delete mutation error feedback", () => {
    handleDeleteKeyError(new Error("permission denied"));
    expect(toastError).toHaveBeenCalledWith("Failed to revoke API key", { description: "permission denied" });
  });
});

