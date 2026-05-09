import { describe, it, expect, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { renderWithProviders } from "../../test-utils/render";
import { server } from "../../test-utils/msw";
import BundleDiffTab from "./BundleDiffTab";

// Stub Monaco DiffEditor — actual editor is heavy in jsdom; we just need
// to verify it gets the `original` + `modified` props the tab assembles.
vi.mock("@monaco-editor/react", () => ({
  __esModule: true,
  DiffEditor: ({ original, modified }: { original: string; modified: string }) => (
    <div data-testid="diff-editor">
      <pre data-testid="diff-original">{original}</pre>
      <pre data-testid="diff-modified">{modified}</pre>
    </div>
  ),
}));

const RULE_V1 = {
  id: "r-1",
  name: "Allow read",
  type: "input",
  scope: { kind: "global" },
  status: "published",
  version: "1",
  audit: { created_at: "2026-05-08T10:00:00Z", created_by: "alice" },
  match: {},
  decide: { type: "allow" },
};

const RULE_V2_MODIFIED = { ...RULE_V1, decide: { type: "deny" } };
const RULE_NEW = { ...RULE_V1, id: "r-2", name: "New rule" };

describe("BundleDiffTab — Dashboard 5 step 8", () => {
  it("renders version pickers when ?from= and ?to= are unset", async () => {
    const { findByLabelText, getByText } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <BundleDiffTab bundleId="b-1" />
      </NuqsTestingAdapter>,
    );
    expect(await findByLabelText("From version")).toBeTruthy();
    expect(getByText("Pick two versions to compare")).toBeTruthy();
  });

  it("renders DiffEditor with YAML rule-snapshots + correct add/remove/modify summary", async () => {
    server.use(
      http.get("*/api/v1/policy/bundles/:id/versions/v1", () =>
        HttpResponse.json({
          version: "v1",
          rule_snapshot: [RULE_V1],
          deployed_at: "2026-05-08T10:00:00Z",
        }),
      ),
      http.get("*/api/v1/policy/bundles/:id/versions/v2", () =>
        HttpResponse.json({
          version: "v2",
          rule_snapshot: [RULE_V2_MODIFIED, RULE_NEW],
          deployed_at: "2026-05-09T10:00:00Z",
        }),
      ),
    );

    const { findByTestId, findByText, getByTestId } = renderWithProviders(
      <NuqsTestingAdapter searchParams="?from=v1&to=v2">
        <BundleDiffTab bundleId="b-1" />
      </NuqsTestingAdapter>,
    );

    // Summary: r-1 modified (decide.type changed), r-2 added.
    expect(await findByText("1 added")).toBeTruthy();
    expect(await findByText("0 removed")).toBeTruthy();
    expect(await findByText("1 modified")).toBeTruthy();

    // Lazy-loaded DiffEditor mounts; YAML serialization passes through.
    const editor = await findByTestId("diff-editor");
    expect(editor).toBeTruthy();
    expect(getByTestId("diff-original").textContent).toContain("r-1");
    expect(getByTestId("diff-modified").textContent).toContain("r-1");
    expect(getByTestId("diff-modified").textContent).toContain("r-2");
  });
});
