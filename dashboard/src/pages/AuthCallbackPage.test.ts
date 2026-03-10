import { describe, it, expect, vi } from "vitest";
import {
  completeAuthCallback,
  buildFallbackCallbackUser,
  type AuthCallbackDeps,
} from "./AuthCallbackPage";

describe("buildFallbackCallbackUser", () => {
  it("builds user from hash params", () => {
    const user = buildFallbackCallbackUser({
      tenant: "acme",
      principalId: "jane",
      role: "admin",
    });
    expect(user).toEqual({
      id: "jane",
      username: "jane",
      email: "jane@local.invalid",
      display_name: "jane",
      roles: ["admin"],
      tenant: "acme",
    });
  });

  it("uses defaults for missing params", () => {
    const user = buildFallbackCallbackUser({
      tenant: null,
      principalId: null,
      role: null,
    });
    expect(user.id).toBe("oidc-user");
    expect(user.tenant).toBe("");
    expect(user.roles).toEqual([]);
  });
});

describe("completeAuthCallback", () => {
  function makeDeps(overrides?: Partial<AuthCallbackDeps>): AuthCallbackDeps {
    return {
      fetchSession: vi.fn().mockResolvedValue({
        user: {
          id: "u1",
          username: "alice",
          email: "alice@acme.co",
          display_name: "Alice",
          roles: ["operator"],
          tenant: "acme",
        },
      }),
      login: vi.fn(),
      ...overrides,
    };
  }

  it("returns error when token is missing", async () => {
    const deps = makeDeps();
    const params = new URLSearchParams("");
    const result = await completeAuthCallback(params, deps);
    expect(result).toEqual({ ok: false, error: "Missing session token." });
    expect(deps.login).not.toHaveBeenCalled();
  });

  it("calls login with session user on success", async () => {
    const deps = makeDeps();
    const params = new URLSearchParams("token=abc123&tenant=acme");
    const result = await completeAuthCallback(params, deps);

    expect(result).toEqual({ ok: true });
    expect(deps.fetchSession).toHaveBeenCalled();
    expect(deps.login).toHaveBeenCalledWith("abc123", {
      id: "u1",
      username: "alice",
      email: "alice@acme.co",
      display_name: "Alice",
      roles: ["operator"],
      tenant: "acme",
    });
  });

  it("falls back to hash params user when fetchSession fails", async () => {
    const deps = makeDeps({
      fetchSession: vi.fn().mockRejectedValue(new Error("network")),
    });
    const params = new URLSearchParams(
      "token=tok999&tenant=corp&principal_id=bob&role=viewer",
    );
    const result = await completeAuthCallback(params, deps);

    expect(result).toEqual({ ok: true });
    expect(deps.login).toHaveBeenCalledWith("tok999", {
      id: "bob",
      username: "bob",
      email: "bob@local.invalid",
      display_name: "bob",
      roles: ["viewer"],
      tenant: "corp",
    });
  });

  it("persists token to localStorage via login()", async () => {
    // Verify login() is called (which internally persists to localStorage)
    const loginSpy = vi.fn();
    const deps = makeDeps({ login: loginSpy });
    const params = new URLSearchParams("token=persist-me");
    await completeAuthCallback(params, deps);

    expect(loginSpy).toHaveBeenCalledTimes(1);
    expect(loginSpy.mock.calls[0][0]).toBe("persist-me");
  });
});
