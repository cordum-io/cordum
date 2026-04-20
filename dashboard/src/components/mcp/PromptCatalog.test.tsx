import React, { act } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { PromptCatalog } from "./PromptCatalog";
import type { McpPrompt } from "@/api/types";

// Fixture mirrors the 4 first-party prompts the gateway's
// PromptRegistry ships with. Kept here (not imported from the hook)
// because useMcpCatalog now does a live fetch and no longer exports a
// constant — the test shouldn't cross that boundary.
const TEST_PROMPT_CATALOG: McpPrompt[] = [
  {
    name: "draft_safety_rule",
    description: "Draft a Cordum policy-bundle YAML scaffold.",
    arguments: [
      { name: "scenario", description: "Plain-text goal.", required: true },
      { name: "topic", description: "Job topic pattern.", required: false },
      { name: "risk_level", description: "low | medium | high.", required: false },
    ],
    modelClass: "small",
    safetyDisclaimer: true,
    docsHref: "/docs/mcp/prompts#draft_safety_rule",
  },
  {
    name: "explain_denial",
    description: "Explain a safety-kernel deny decision.",
    arguments: [{ name: "job_id", description: "Job id.", required: true }],
    modelClass: "small",
    safetyDisclaimer: false,
    docsHref: "/docs/mcp/prompts#explain_denial",
  },
  {
    name: "summarize_approvals",
    description: "Summarise approval activity in a time window.",
    arguments: [
      { name: "window", description: "24h to 30d.", required: false },
      { name: "tenant", description: "Tenant filter.", required: false },
    ],
    modelClass: "reasoning",
    safetyDisclaimer: false,
    docsHref: "/docs/mcp/prompts#summarize_approvals",
  },
  {
    name: "policy_migration_helper",
    description: "Convert a bundle between grammar versions.",
    arguments: [
      { name: "from_version", description: "Source grammar.", required: true },
      { name: "to_version", description: "Target grammar.", required: true },
    ],
    modelClass: "reasoning",
    safetyDisclaimer: true,
    docsHref: "/docs/mcp/prompts#policy_migration_helper",
  },
];

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface Ctx {
  container: HTMLElement;
  root: Root;
}
const mounted: Ctx[] = [];
function render(ui: React.ReactElement): Ctx {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => root.render(ui));
  const ctx = { container, root };
  mounted.push(ctx);
  return ctx;
}
afterEach(() => {
  while (mounted.length > 0) {
    const ctx = mounted.pop();
    if (!ctx) continue;
    act(() => ctx.root.unmount());
    ctx.container.remove();
  }
});

describe("PromptCatalog", () => {
  it("renders the four first-party prompts with their argument schemas", () => {
    const ctx = render(
      <PromptCatalog prompts={TEST_PROMPT_CATALOG} isLoading={false} error={null} />,
    );
    const root = ctx.container.querySelector<HTMLElement>(
      "[data-testid='mcp-prompt-catalog']",
    );
    expect(root).not.toBeNull();
    // Card presence for each prompt name.
    for (const name of [
      "draft_safety_rule",
      "explain_denial",
      "summarize_approvals",
      "policy_migration_helper",
    ]) {
      const card = ctx.container.querySelector(
        `[data-testid='mcp-prompt-card-${name}']`,
      );
      expect(card, `card for ${name}`).not.toBeNull();
    }
    // Required arguments surface.
    expect(ctx.container.textContent).toContain("scenario");
    expect(ctx.container.textContent).toContain("job_id");
    expect(ctx.container.textContent).toContain("from_version");
  });

  it("tags safety-disclaimer prompts with a 'Simulate first' chip", () => {
    const ctx = render(
      <PromptCatalog prompts={TEST_PROMPT_CATALOG} isLoading={false} error={null} />,
    );
    // Two of the four prompts carry safetyDisclaimer=true.
    const disclaimerChips = Array.from(
      ctx.container.querySelectorAll<HTMLElement>(
        "[aria-label='Simulate output before applying to production']",
      ),
    );
    expect(disclaimerChips.length).toBe(2);
  });

  it("renders a loading skeleton grid while prompts are fetching", () => {
    const ctx = render(
      <PromptCatalog prompts={undefined} isLoading={true} error={null} />,
    );
    // No card grid yet.
    expect(
      ctx.container.querySelector("[data-testid='mcp-prompt-grid']"),
    ).toBeNull();
    // Section still rendered.
    expect(
      ctx.container.querySelector("[data-testid='mcp-prompt-catalog']"),
    ).not.toBeNull();
  });

  it("renders an empty state when the gateway reports no prompts registered", () => {
    const ctx = render(
      <PromptCatalog prompts={[]} isLoading={false} error={null} />,
    );
    expect(ctx.container.textContent).toContain("No MCP prompts registered");
  });

  it("renders an error banner when the hook reports an error", () => {
    const ctx = render(
      <PromptCatalog
        prompts={undefined}
        isLoading={false}
        error={new Error("lookup timeout")}
      />,
    );
    expect(ctx.container.textContent).toContain("Unable to load MCP prompts");
    expect(ctx.container.textContent).toContain("lookup timeout");
  });

  it("filter input narrows the card grid", () => {
    const ctx = render(
      <PromptCatalog prompts={TEST_PROMPT_CATALOG} isLoading={false} error={null} />,
    );
    const filter = ctx.container.querySelector<HTMLInputElement>(
      "[data-testid='mcp-prompt-filter']",
    );
    expect(filter).not.toBeNull();
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLInputElement.prototype,
        "value",
      )!.set!;
      setter.call(filter, "migration");
      filter!.dispatchEvent(new Event("input", { bubbles: true }));
    });
    // Only policy_migration_helper matches — count the card elements
    // themselves (Card wrapper has data-testid=`mcp-prompt-card-<name>`)
    // while excluding the inner docs-link element that shares the prefix.
    const visibleCards = Array.from(
      ctx.container.querySelectorAll<HTMLElement>(
        "[data-testid^='mcp-prompt-card-']",
      ),
    ).filter((el) => !el.getAttribute("data-testid")?.endsWith("-docs"));
    expect(visibleCards.length).toBe(1);
    expect(visibleCards[0].getAttribute("data-testid")).toBe(
      "mcp-prompt-card-policy_migration_helper",
    );
  });

  it("each prompt card links to its docs entry via docsHref", () => {
    const ctx = render(
      <PromptCatalog prompts={TEST_PROMPT_CATALOG} isLoading={false} error={null} />,
    );
    const link = ctx.container.querySelector<HTMLAnchorElement>(
      "[data-testid='mcp-prompt-card-draft_safety_rule-docs']",
    );
    expect(link?.getAttribute("href")).toBe("/docs/mcp/prompts#draft_safety_rule");
  });
});
