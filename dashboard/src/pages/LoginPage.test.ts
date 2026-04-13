import { describe, it, expect, vi } from "vitest";
import {
  isSafeReturnUrl,
  isSafeApiUrl,
  buildOidcLoginHref,
  buildSamlLoginHref,
  buildSamlRedirectTarget,
  buildPasswordFallbackUser,
  parseSamlCallbackHash,
  parseLoginUser,
  readJsonIfOk,
} from "./LoginPage";

describe("isSafeReturnUrl", () => {
  it("accepts valid relative paths", () => {
    expect(isSafeReturnUrl("/")).toBe("/");
    expect(isSafeReturnUrl("/dashboard")).toBe("/dashboard");
    expect(isSafeReturnUrl("/jobs?status=failed")).toBe("/jobs?status=failed");
    expect(isSafeReturnUrl("/workflows/abc/runs/123")).toBe(
      "/workflows/abc/runs/123",
    );
  });

  it("rejects null/undefined/empty", () => {
    expect(isSafeReturnUrl(null)).toBe("/");
    expect(isSafeReturnUrl("")).toBe("/");
    expect(isSafeReturnUrl("  ")).toBe("/");
  });

  it("rejects absolute URLs (open redirect)", () => {
    expect(isSafeReturnUrl("https://evil.com")).toBe("/");
    expect(isSafeReturnUrl("http://evil.com")).toBe("/");
    expect(isSafeReturnUrl("ftp://evil.com")).toBe("/");
  });

  it("rejects protocol-relative URLs", () => {
    expect(isSafeReturnUrl("//evil.com")).toBe("/");
    expect(isSafeReturnUrl("//evil.com/path")).toBe("/");
  });

  it("rejects javascript: and data: schemes", () => {
    expect(isSafeReturnUrl("javascript:alert(1)")).toBe("/");
    expect(isSafeReturnUrl("data:text/html,<script>")).toBe("/");
  });

  it("rejects URLs with embedded whitespace or colons", () => {
    expect(isSafeReturnUrl("/foo bar")).toBe("/");
    expect(isSafeReturnUrl("/foo:bar")).toBe("/");
  });
});

describe("isSafeApiUrl", () => {
  it("accepts relative paths", () => {
    expect(isSafeApiUrl("/api/v1")).toBe("/api/v1");
    expect(isSafeApiUrl("/custom/endpoint")).toBe("/custom/endpoint");
  });

  it("returns fallback for empty input", () => {
    expect(isSafeApiUrl("")).toBe("/api/v1");
    expect(isSafeApiUrl("  ")).toBe("/api/v1");
  });

  it("accepts same-origin absolute URL", () => {
    // window.location.origin is "http://localhost:3000" in vitest/jsdom
    const origin = window.location.origin;
    expect(isSafeApiUrl(`${origin}/api/v1`)).toBe(`${origin}/api/v1`);
  });

  it("rejects external origin (open redirect)", () => {
    expect(isSafeApiUrl("https://attacker.com")).toBe("/api/v1");
    expect(isSafeApiUrl("https://evil.com/api/v1")).toBe("/api/v1");
    expect(isSafeApiUrl("http://phishing.site")).toBe("/api/v1");
  });

  it("rejects javascript: protocol", () => {
    expect(isSafeApiUrl("javascript:alert(1)")).toBe("/api/v1");
  });

  it("rejects data: protocol", () => {
    expect(isSafeApiUrl("data:text/html,<script>alert(1)</script>")).toBe(
      "/api/v1",
    );
  });

  it("rejects protocol-relative URLs (//)", () => {
    expect(isSafeApiUrl("//evil.com")).toBe("/api/v1");
    expect(isSafeApiUrl("//evil.com/api")).toBe("/api/v1");
  });
});

describe("buildPasswordFallbackUser", () => {
  it("assigns viewer role, not admin", () => {
    const user = buildPasswordFallbackUser("bob");
    expect(user.roles).toEqual(["viewer"]);
    expect(user.roles).not.toContain("admin");
  });

  it("trims username and sets correct fields", () => {
    const user = buildPasswordFallbackUser("  alice  ");
    expect(user.id).toBe("alice");
    expect(user.username).toBe("alice");
    expect(user.display_name).toBe("alice");
    expect(user.tenant).toBe("default");
    expect(user.email).toBe("");
  });
});

describe("parseSamlCallbackHash", () => {
  it("parses a successful SAML callback fragment into a session payload", () => {
    expect(
      parseSamlCallbackHash(
        "#token=session-123&user_id=user-1&username=alice&email=alice%40example.com&display_name=Alice&role=admin&tenant=acme",
      ),
    ).toEqual({
      token: "session-123",
      user: {
        id: "user-1",
        username: "alice",
        email: "alice@example.com",
        display_name: "Alice",
        roles: ["admin"],
        tenant: "acme",
      },
    });
  });

  it("falls back to a viewer role and derives a username when needed", () => {
    expect(
      parseSamlCallbackHash("#token=session-123&email=alice%40example.com"),
    ).toEqual({
      token: "session-123",
      user: {
        id: "alice@example.com",
        username: "alice@example.com",
        email: "alice@example.com",
        display_name: "alice@example.com",
        roles: ["viewer"],
        tenant: "default",
      },
    });
  });

  it("returns null when the fragment is missing a token or user identity", () => {
    expect(parseSamlCallbackHash("#username=alice")).toBeNull();
    expect(parseSamlCallbackHash("#token=session-123")).toBeNull();
  });
});

describe("parseLoginUser", () => {
  it("returns a typed user when the payload is complete", () => {
    expect(
      parseLoginUser({
        id: "user-1",
        username: "alice",
        email: "alice@example.com",
        display_name: "Alice",
        roles: ["admin"],
        tenant: "default",
      }),
    ).toEqual({
      id: "user-1",
      username: "alice",
      email: "alice@example.com",
      display_name: "Alice",
      roles: ["admin"],
      tenant: "default",
    });
  });

  it("returns null when required user fields are missing", () => {
    expect(
      parseLoginUser({
        username: "alice",
        roles: ["admin"],
      }),
    ).toBeNull();
  });
});

describe("SAML redirect helpers", () => {
  it("builds a login redirect target that returns to /login with the original path", () => {
    expect(buildSamlRedirectTarget("/jobs?status=failed")).toBe(
      `${window.location.origin}/login?returnUrl=%2Fjobs%3Fstatus%3Dfailed`,
    );
  });

  it("builds a safe SAML login href against the chosen API base URL", () => {
    expect(buildSamlLoginHref("/api/v1", "/jobs")).toBe(
      `${window.location.origin}/api/v1/auth/sso/saml/login?redirect=${encodeURIComponent(`${window.location.origin}/login?returnUrl=%2Fjobs`)}`,
    );
  });

  it("builds a safe OIDC login href against the chosen API base URL", () => {
    expect(buildOidcLoginHref("/api/v1", "/jobs")).toBe(
      `${window.location.origin}/api/v1/auth/sso/oidc/login?redirect=${encodeURIComponent(`${window.location.origin}/login?returnUrl=%2Fjobs`)}`,
    );
  });
});

describe("readJsonIfOk", () => {
  it("does not read JSON when the response is not ok", async () => {
    const json = vi.fn();
    await expect(
      readJsonIfOk({ ok: false, json } as unknown as Response),
    ).resolves.toBeNull();
    expect(json).not.toHaveBeenCalled();
  });

  it("returns null when JSON parsing fails on a success response", async () => {
    const json = vi.fn().mockRejectedValue(new Error("bad json"));
    await expect(
      readJsonIfOk({ ok: true, json } as unknown as Response),
    ).resolves.toBeNull();
    expect(json).toHaveBeenCalledTimes(1);
  });
});
