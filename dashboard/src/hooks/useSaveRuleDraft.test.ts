import { beforeAll, describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { ensureMswServerListening, server } from "@/test-utils/msw";
import { renderWithQueryClient } from "./__tests__/test-utils";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import { useSaveRuleDraft } from "./useSaveRuleDraft";
import { UNKNOWN_RULE_TYPE, type NormalizedRule } from "./useRulesList";

const sampleRule: NormalizedRule = {
  id: "rule.demo",
  name: "Block secrets",
  type: RuleType.input,
  scope: { kind: RuleScopeKind.global },
  status: RuleStatus.draft,
  version: "v1",
  audit: { created_at: "2026-05-10T12:00:00Z", created_by: "alice" },
  match: { topics: ["job.acme.evaluate"] },
  decide: { decision: "deny", reason: "demo" },
};

describe("useSaveRuleDraft (Phase 3E)", () => {
  // The hook test scaffolding (renderWithQueryClient) doesn't auto-start
  // MSW the way renderWithProviders does for page tests, so we boot the
  // server explicitly. Kept idempotent — second call short-circuits.
  beforeAll(() => {
    ensureMswServerListening();
  });

  it("create path: POST /policy/rules returns ok=true with the persisted Rule", async () => {
    const hook = renderWithQueryClient(() => useSaveRuleDraft());
    const outcome = await hook.result.current!.mutateAsync({ mode: "create", rule: sampleRule });
    if (!outcome.ok) throw new Error(`expected ok, got ${JSON.stringify(outcome)}`);
    expect(outcome.rule.id).toBe("rule.demo");
    expect(outcome.rule.version).toBe("v1");
    hook.unmount();
  });

  it("update path: PUT with If-Match=v1 returns ok=true with version bumped to v2", async () => {
    const hook = renderWithQueryClient(() => useSaveRuleDraft());
    const outcome = await hook.result.current!.mutateAsync({
      mode: "update",
      rule: sampleRule,
      ifMatch: "v1",
    });
    if (!outcome.ok) throw new Error(`expected ok, got ${JSON.stringify(outcome)}`);
    expect(outcome.rule.version).toBe("v2");
    hook.unmount();
  });

  it("update with stale If-Match: returns kind=stale + currentVersion + currentAuditHash", async () => {
    const hook = renderWithQueryClient(() => useSaveRuleDraft());
    // Default MSW handler in test-utils/handlers.ts returns 409 stale_version
    // when If-Match !== "v1". We send "v0-stale" to exercise that path.
    const outcome = await hook.result.current!.mutateAsync({
      mode: "update",
      rule: sampleRule,
      ifMatch: "v0-stale",
    });
    if (outcome.ok) throw new Error("expected stale rejection, got ok");
    expect(outcome.kind).toBe("stale");
    if (outcome.kind !== "stale") return;
    expect(outcome.currentVersion).toBe("v2");
    expect(outcome.currentAuditHash).toBeTruthy();
    hook.unmount();
  });

  it("create path: 409 duplicate id returns kind=validation", async () => {
    server.use(
      http.post("*/api/v1/policy/rules", () =>
        HttpResponse.json({ error: "rule already exists" }, { status: 409 }),
      ),
    );
    const hook = renderWithQueryClient(() => useSaveRuleDraft());
    const outcome = await hook.result.current!.mutateAsync({ mode: "create", rule: sampleRule });
    if (outcome.ok) throw new Error("expected validation rejection, got ok");
    expect(outcome.kind).toBe("validation");
    hook.unmount();
  });

  it("update on missing rule: 404 returns kind=unknown (typed)", async () => {
    server.use(
      http.put("*/api/v1/policy/rules/:id", () =>
        HttpResponse.json({ error: "not found" }, { status: 404 }),
      ),
    );
    const hook = renderWithQueryClient(() => useSaveRuleDraft());
    const outcome = await hook.result.current!.mutateAsync({
      mode: "update",
      rule: sampleRule,
      ifMatch: "v1",
    });
    if (outcome.ok) throw new Error("expected unknown error, got ok");
    expect(outcome.kind).toBe("unknown");
    hook.unmount();
  });

  it("permission denied: 403 returns kind=permission", async () => {
    server.use(
      http.post("*/api/v1/policy/rules", () =>
        HttpResponse.json({ error: "forbidden" }, { status: 403 }),
      ),
    );
    const hook = renderWithQueryClient(() => useSaveRuleDraft());
    const outcome = await hook.result.current!.mutateAsync({ mode: "create", rule: sampleRule });
    if (outcome.ok) throw new Error("expected permission rejection, got ok");
    expect(outcome.kind).toBe("permission");
    hook.unmount();
  });

  it("rejects unknown-type rule before any network call (failed-fast)", async () => {
    const hook = renderWithQueryClient(() => useSaveRuleDraft());
    const unknownTypeRule: NormalizedRule = { ...sampleRule, type: UNKNOWN_RULE_TYPE };
    const outcome = await hook.result.current!.mutateAsync({
      mode: "create",
      rule: unknownTypeRule,
    });
    if (outcome.ok) throw new Error("expected validation rejection for unknown type");
    expect(outcome.kind).toBe("validation");
    hook.unmount();
  });
});
