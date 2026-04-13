import { beforeEach, describe, expect, it, vi } from "vitest";
import { logger } from "./logger";
import { safeLocalStorage } from "./storage";

describe("safeLocalStorage", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("reads stored values when localStorage works", () => {
    window.localStorage.setItem("theme", "dark");
    expect(safeLocalStorage.getItem("theme")).toBe("dark");
  });

  it("returns null and logs when getItem throws", () => {
    const warnSpy = vi.spyOn(logger, "warn").mockImplementation(() => {});
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("private mode denied");
    });

    expect(safeLocalStorage.getItem("theme")).toBeNull();
    expect(warnSpy).toHaveBeenCalledWith(
      "storage",
      "localStorage getItem failed",
      expect.objectContaining({ key: "theme", error: "private mode denied" }),
    );
  });

  it("returns false and logs when setItem throws", () => {
    const warnSpy = vi.spyOn(logger, "warn").mockImplementation(() => {});
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota exceeded");
    });

    expect(safeLocalStorage.setItem("theme", "dark")).toBe(false);
    expect(warnSpy).toHaveBeenCalledWith(
      "storage",
      "localStorage setItem failed",
      expect.objectContaining({ key: "theme", error: "quota exceeded" }),
    );
  });
});
