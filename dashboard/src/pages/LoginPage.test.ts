import { describe, it, expect } from "vitest";
import { isSafeReturnUrl, isSafeApiUrl, buildPasswordFallbackUser } from "./LoginPage";

describe("isSafeReturnUrl", () => {
  it("accepts valid relative paths", () => {
    expect(isSafeReturnUrl("/")).toBe("/");
    expect(isSafeReturnUrl("/dashboard")).toBe("/dashboard");
    expect(isSafeReturnUrl("/jobs?status=failed")).toBe("/jobs?status=failed");
    expect(isSafeReturnUrl("/workflows/abc/runs/123")).toBe("/workflows/abc/runs/123");
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
    expect(isSafeApiUrl("data:text/html,<script>alert(1)</script>")).toBe("/api/v1");
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
