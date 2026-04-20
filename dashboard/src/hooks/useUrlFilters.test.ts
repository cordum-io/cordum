import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithQueryClient } from "./__tests__/test-utils";
import { __urlFiltersInternal, useUrlFilters } from "./useUrlFilters";

const { getParams, resetParams, setParams } = vi.hoisted(() => {
  let params = new URLSearchParams();
  const getParams = () => params;
  const resetParams = (initial = "") => {
    params = new URLSearchParams(initial);
  };
  const setParams = (
    updater:
      | URLSearchParams
      | string
      | Record<string, string>
      | ((prev: URLSearchParams) => URLSearchParams),
  ) => {
    const next = typeof updater === "function" ? updater(params) : updater;
    params = new URLSearchParams(next as URLSearchParams | string | Record<string, string>);
  };
  return { getParams, resetParams, setParams };
});

vi.mock("react-router-dom", () => ({
  useSearchParams: () => [getParams(), setParams],
}));

describe("useUrlFilters internals", () => {
  it("parseList handles null, empty, values, and trailing commas", () => {
    expect(__urlFiltersInternal.parseList(null)).toEqual([]);
    expect(__urlFiltersInternal.parseList("")).toEqual([]);
    expect(__urlFiltersInternal.parseList("a,b,c")).toEqual(["a", "b", "c"]);
    expect(__urlFiltersInternal.parseList("a,b,,")).toEqual(["a", "b"]);
  });

  it("parseList rejects params over 1000 chars", () => {
    const oversized = "x".repeat(1001);
    expect(__urlFiltersInternal.parseList(oversized)).toEqual([]);
  });

  it("parseList caps items at 50", () => {
    const items = Array.from({ length: 80 }, (_, i) => `item${i}`).join(",");
    const result = __urlFiltersInternal.parseList(items);
    expect(result).toHaveLength(50);
    expect(result[0]).toBe("item0");
    expect(result[49]).toBe("item49");
  });

  it("serializeValue handles string arrays, numbers, nullable numbers, and strings", () => {
    expect(__urlFiltersInternal.serializeValue("string[]", ["a", "b"])).toBe("a,b");
    expect(__urlFiltersInternal.serializeValue("number", 42)).toBe("42");
    expect(__urlFiltersInternal.serializeValue("number", null)).toBe("");
    expect(__urlFiltersInternal.serializeValue("string", "hello")).toBe("hello");
  });

  it("parseValue handles string arrays, numbers, and strings", () => {
    expect(__urlFiltersInternal.parseValue("string[]", "a,b")).toEqual(["a", "b"]);
    expect(__urlFiltersInternal.parseValue("number", "12")).toBe(12);
    expect(__urlFiltersInternal.parseValue("number", "not-a-number")).toBeUndefined();
    expect(__urlFiltersInternal.parseValue("number", "")).toBeUndefined();
    expect(__urlFiltersInternal.parseValue("string", "hello")).toBe("hello");
    expect(__urlFiltersInternal.parseValue("string", null)).toBe("");
  });

  it("parseValue returns type-appropriate defaults for oversized params", () => {
    const oversized = "x".repeat(1001);
    expect(__urlFiltersInternal.parseValue("string[]", oversized)).toEqual([]);
    expect(__urlFiltersInternal.parseValue("number", oversized)).toBeUndefined();
    expect(__urlFiltersInternal.parseValue("string", oversized)).toBe("");
  });
});

describe("useUrlFilters hook", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    resetParams("");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("parses schema values from URL params", () => {
    resetParams("q=hello&tags=a,b&page=3");

    const hook = renderWithQueryClient(() =>
      useUrlFilters(
        {
          q: "string",
          tags: "string[]",
          page: "number",
        },
        { resetPage: true },
      ),
    );

    const [filters] = hook.result.current ?? [];
    expect(filters).toEqual({ q: "hello", tags: ["a", "b"], page: 3 });
    hook.unmount();
  });

  it("setFilter updates params and resets page to 1", () => {
    resetParams("q=old&page=5");

    const hook = renderWithQueryClient(() =>
      useUrlFilters({ q: "string", page: "number" }, { resetPage: true }),
    );

    act(() => {
      hook.result.current?.[1]("q", "new-value");
    });
    hook.rerender();

    expect(getParams().get("q")).toBe("new-value");
    expect(getParams().get("page")).toBe("1");
    hook.unmount();
  });

  it("setFilterDebounced waits and then applies update", () => {
    vi.useFakeTimers();
    resetParams("q=old&page=5");

    const hook = renderWithQueryClient(() =>
      useUrlFilters({ q: "string", page: "number" }, { resetPage: true }),
    );

    act(() => {
      hook.result.current?.[2]("q", "debounced", 250);
    });

    expect(getParams().get("q")).toBe("old");

    act(() => {
      vi.advanceTimersByTime(250);
    });
    hook.rerender();

    expect(getParams().get("q")).toBe("debounced");
    expect(getParams().get("page")).toBe("1");

    hook.unmount();
    vi.useRealTimers();
  });

  it("clearAll removes schema params and preserves requested params", () => {
    resetParams("q=hello&tags=a,b&page=3&tab=overview");

    const hook = renderWithQueryClient(() =>
      useUrlFilters(
        { q: "string", tags: "string[]", page: "number" },
        { preserveParams: ["tab"] },
      ),
    );

    act(() => {
      hook.result.current?.[3]();
    });
    hook.rerender();

    expect(getParams().get("q")).toBeNull();
    expect(getParams().get("tags")).toBeNull();
    expect(getParams().get("page")).toBeNull();
    expect(getParams().get("tab")).toBe("overview");
    hook.unmount();
  });

  it("clears pending debounce timers on unmount", () => {
    vi.useFakeTimers();
    resetParams("");

    const hook = renderWithQueryClient(() =>
      useUrlFilters({ q: "string" }, { resetPage: true }),
    );

    act(() => {
      hook.result.current?.[2]("q", "pending-value", 500);
    });

    // Unmount before the timer fires
    hook.unmount();

    act(() => {
      vi.advanceTimersByTime(600);
    });

    // The debounced setSearchParams should NOT have been called
    expect(getParams().get("q")).toBeNull();
    vi.useRealTimers();
  });

  it("activeCount counts non-empty schema params", () => {
    resetParams("q=hello&tags=a,b&page=3&empty=");

    const hook = renderWithQueryClient(() =>
      useUrlFilters({ q: "string", tags: "string[]", page: "number", empty: "string" }),
    );

    expect(hook.result.current?.[4]).toBe(3);
    hook.unmount();
  });
});
