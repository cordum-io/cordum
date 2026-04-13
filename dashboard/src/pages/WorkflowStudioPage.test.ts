import { describe, expect, it } from "vitest";
import { WORKFLOW_STUDIO_PAGE_STYLE } from "./WorkflowStudioPage";

describe("WORKFLOW_STUDIO_PAGE_STYLE", () => {
  it("uses dvh for mobile viewport height with a vh fallback", () => {
    expect(WORKFLOW_STUDIO_PAGE_STYLE.height).toBe("calc(100dvh - 3rem)");
    expect(WORKFLOW_STUDIO_PAGE_STYLE.minHeight).toBe("calc(100vh - 3rem)");
  });
});
