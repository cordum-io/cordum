import { describe, it, expect, beforeEach, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { fireEvent, screen, waitFor } from "@testing-library/dom";
import { renderWithProviders } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import { DecisionSource } from "@/api/generated/model/decisionSource";
import { DecisionType } from "@/api/generated/model/decisionType";
import { RuleType } from "@/api/generated/model/ruleType";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import type { Decision } from "@/api/generated/model/decision";
import { WhatIfDrawer } from "./WhatIfDrawer";

const decision: Decision = {
  source: DecisionSource.job,
  rule_id: "rule.input.secret-scan",
  bundle_id: "bundle.acme.input",
  bundle_version: "v3",
  type: DecisionType.deny,
  timestamp: "2026-05-10T12:00:00Z",
  audit_hash: "sha256:whatif",
  input_ref: "blob://acme/in/01HQ",
};

const ruleFixture = {
  id: "rule.input.secret-scan",
  name: "Block secrets",
  type: RuleType.input,
  scope: { kind: RuleScopeKind.global },
  status: RuleStatus.published,
  version: "v3",
  audit: {
    created_at: "2026-05-09T00:00:00Z",
    updated_at: "2026-05-10T00:00:00Z",
    created_by: "alice",
    updated_by: "alice",
  },
  match: { secret_pattern: "aws-access-key" },
  decide: { type: "deny", reason: "secret detected" },
};

function mockRulesList() {
  return http.get("*/api/v1/policy/rules", () =>
    HttpResponse.json({
      items: [ruleFixture],
      has_more: false,
      next_cursor: "",
    }),
  );
}

describe("WhatIfDrawer (D9b — Monaco edit + re-evaluate, no-save)", () => {
  beforeEach(() => {
    server.resetHandlers();
  });

  it("renders nothing when open=false", () => {
    server.use(mockRulesList());
    const { container } = renderWithProviders(
      <WhatIfDrawer open={false} onClose={() => {}} decision={decision} />,
    );
    expect(container.querySelector('[role="dialog"]')).toBeNull();
  });

  it("opens as a dialog labeled with the rule id when open=true", async () => {
    server.use(mockRulesList());
    renderWithProviders(
      <WhatIfDrawer open onClose={() => {}} decision={decision} />,
    );
    const dialog = await screen.findByRole("dialog");
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    // Drawer label includes the rule id so screen-reader users know which
    // rule they're hypothetically editing.
    expect((dialog.getAttribute("aria-label") ?? "")).toMatch(
      /rule\.input\.secret-scan/,
    );
  });

  it("shows a loading state until the rule fetch resolves", async () => {
    let release: (() => void) | null = null;
    server.use(
      http.get("*/api/v1/policy/rules", async () => {
        await new Promise<void>((resolve) => {
          release = resolve;
        });
        return HttpResponse.json({ items: [ruleFixture], has_more: false, next_cursor: "" });
      }),
    );
    renderWithProviders(
      <WhatIfDrawer open onClose={() => {}} decision={decision} />,
    );
    expect(await screen.findByTestId("whatif-rule-loading")).not.toBeNull();
    (release as (() => void) | null)?.();
  });

  it("renders an actual-decision panel summarizing the original outcome", async () => {
    server.use(mockRulesList());
    renderWithProviders(
      <WhatIfDrawer open onClose={() => {}} decision={decision} />,
    );
    const actual = await screen.findByTestId("whatif-actual");
    // Actual panel must surface the original decision type so the user
    // can compare the hypothetical against the truth-on-the-wire.
    expect(actual.textContent ?? "").toMatch(/deny/i);
  });

  it("Re-evaluate button calls /api/v1/policy/evaluate and renders the hypothetical decision", async () => {
    let evaluatePosted = false;
    server.use(
      mockRulesList(),
      http.post("*/api/v1/policy/evaluate", () => {
        evaluatePosted = true;
        return HttpResponse.json({
          decision: {
            type: "allow",
            rule_id: ruleFixture.id,
            timestamp: "2026-05-10T12:00:01Z",
            source: "job",
          },
        });
      }),
    );
    renderWithProviders(
      <WhatIfDrawer open onClose={() => {}} decision={decision} />,
    );

    // Wait for the rule fetch to land so the Re-evaluate button leaves
    // its disabled-pending-rule state (Phase 4b — no eval before draft).
    await waitFor(() => {
      expect(screen.queryByTestId("whatif-rule-loading")).toBeNull();
    });
    const reeval = await screen.findByRole("button", { name: /re-?evaluate/i });
    expect(reeval.hasAttribute("disabled")).toBe(false);
    fireEvent.click(reeval);

    await waitFor(() => {
      expect(evaluatePosted).toBe(true);
    });
    const hypothetical = await screen.findByTestId("whatif-hypothetical");
    expect(hypothetical.textContent ?? "").toMatch(/allow/i);
  });

  it("close discards edits and never invokes the rule update endpoint (no-save semantics)", async () => {
    let putCalled = false;
    let postCalled = false;
    server.use(
      mockRulesList(),
      http.put("*/api/v1/policy/rules/:id", () => {
        putCalled = true;
        return HttpResponse.json({ ...ruleFixture, version: "v4" });
      }),
      http.post("*/api/v1/policy/rules", () => {
        postCalled = true;
        return HttpResponse.json({ ...ruleFixture }, { status: 201 });
      }),
    );

    const onClose = vi.fn();
    renderWithProviders(
      <WhatIfDrawer open onClose={onClose} decision={decision} />,
    );

    const close = await screen.findByRole("button", {
      name: /close what-if drawer/i,
    });
    fireEvent.click(close);

    await waitFor(() => {
      expect(onClose).toHaveBeenCalledTimes(1);
    });
    // Save mutations MUST NEVER fire as a side-effect of closing the
    // drawer. Spec § L141 (no-save) is the load-bearing contract for
    // this surface; QA rejects on any leak.
    expect(putCalled).toBe(false);
    expect(postCalled).toBe(false);
  });

  it("renders an error message when the rule fetch fails", async () => {
    server.use(
      http.get("*/api/v1/policy/rules", () =>
        HttpResponse.json({ error: "internal" }, { status: 500 }),
      ),
    );
    renderWithProviders(
      <WhatIfDrawer open onClose={() => {}} decision={decision} />,
    );

    const error = await screen.findByTestId("whatif-rule-error");
    expect(error.textContent ?? "").toMatch(/couldn't|error|failed/i);
  });
});
