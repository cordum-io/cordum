import { beforeAll, describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { ensureMswServerListening, server } from "@/test-utils/msw";
import { renderWithQueryClient } from "./__tests__/test-utils";
import { useAddRuleToBundle } from "./useAddRuleToBundle";

describe("useAddRuleToBundle (Phase 3E)", () => {
  beforeAll(() => {
    ensureMswServerListening();
  });

  it("happy path: returns ok=true with the updated Bundle", async () => {
    const hook = renderWithQueryClient(() => useAddRuleToBundle());
    const result = await hook.result.current!.mutateAsync({
      bundleId: "bundle-1",
      ruleId: "rule.demo",
    });
    if (!result.ok) throw new Error(`expected ok, got ${JSON.stringify(result)}`);
    expect(result.bundle.id).toBe("bundle-1");
    expect(result.bundle.rule_ids).toContain("rule.demo");
    hook.unmount();
  });

  it("404 with bundle_not_found: returns kind=bundle_not_found (disambiguated)", async () => {
    server.use(
      http.post("*/api/v1/policy/bundles/:id/rules", () =>
        HttpResponse.json({ error: "bundle_not_found" }, { status: 404 }),
      ),
    );
    const hook = renderWithQueryClient(() => useAddRuleToBundle());
    const result = await hook.result.current!.mutateAsync({
      bundleId: "missing-bundle",
      ruleId: "rule.demo",
    });
    if (result.ok) throw new Error("expected bundle_not_found, got ok");
    expect(result.kind).toBe("bundle_not_found");
    hook.unmount();
  });

  it("404 with rule_not_found: returns kind=rule_not_found (disambiguated)", async () => {
    server.use(
      http.post("*/api/v1/policy/bundles/:id/rules", () =>
        HttpResponse.json({ error: "rule_not_found" }, { status: 404 }),
      ),
    );
    const hook = renderWithQueryClient(() => useAddRuleToBundle());
    const result = await hook.result.current!.mutateAsync({
      bundleId: "bundle-1",
      ruleId: "missing-rule",
    });
    if (result.ok) throw new Error("expected rule_not_found, got ok");
    expect(result.kind).toBe("rule_not_found");
    hook.unmount();
  });

  it("403: returns kind=permission", async () => {
    server.use(
      http.post("*/api/v1/policy/bundles/:id/rules", () =>
        HttpResponse.json({ error: "forbidden" }, { status: 403 }),
      ),
    );
    const hook = renderWithQueryClient(() => useAddRuleToBundle());
    const result = await hook.result.current!.mutateAsync({
      bundleId: "bundle-1",
      ruleId: "rule.demo",
    });
    if (result.ok) throw new Error("expected permission denied, got ok");
    expect(result.kind).toBe("permission");
    hook.unmount();
  });

  it("idempotent: repeating same add does not create duplicates", async () => {
    const hook = renderWithQueryClient(() => useAddRuleToBundle());
    const first = await hook.result.current!.mutateAsync({
      bundleId: "bundle-1",
      ruleId: "rule.demo",
    });
    const second = await hook.result.current!.mutateAsync({
      bundleId: "bundle-1",
      ruleId: "rule.demo",
    });
    if (!first.ok || !second.ok) {
      throw new Error("expected both adds ok");
    }
    // The default MSW handler returns rule_ids: [body.rule_id] every time,
    // so length should stay 1 — no client-side duplicates introduced.
    expect(first.bundle.rule_ids).toEqual(second.bundle.rule_ids);
    hook.unmount();
  });
});
