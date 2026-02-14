import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { renderWithQueryClient } from "./__tests__/test-utils";
import { usePageTitle } from "./usePageTitle";

describe("usePageTitle", () => {
  beforeEach(() => {
    document.title = "Cordum Control Plane";
  });

  afterEach(() => {
    document.title = "Cordum Control Plane";
  });

  it("sets document title with Cordum suffix", async () => {
    const hook = renderWithQueryClient(() => usePageTitle("Jobs"));

    await hook.waitFor(() => {
      expect(document.title).toBe("Jobs | Cordum");
    });

    hook.unmount();
  });

  it("falls back to default title for empty input", async () => {
    const hook = renderWithQueryClient(() => usePageTitle(""));

    await hook.waitFor(() => {
      expect(document.title).toBe("Cordum Control Plane");
    });

    hook.unmount();
  });

  it("restores default title on unmount cleanup", async () => {
    const hook = renderWithQueryClient(() => usePageTitle("Audit"));

    await hook.waitFor(() => {
      expect(document.title).toBe("Audit | Cordum");
    });

    hook.unmount();
    expect(document.title).toBe("Cordum Control Plane");
  });
});
