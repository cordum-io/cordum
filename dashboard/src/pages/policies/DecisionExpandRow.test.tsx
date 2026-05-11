import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { renderWithProviders } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import { DecisionSource } from "@/api/generated/model/decisionSource";
import { DecisionType } from "@/api/generated/model/decisionType";
import type { Decision } from "@/api/generated/model/decision";
import { DecisionExpandRow } from "./DecisionExpandRow";

const baseDecision: Decision = {
  source: DecisionSource.job,
  rule_id: "rule.input.secret-scan",
  bundle_id: "bundle.acme.input",
  bundle_version: "v3",
  type: DecisionType.deny,
  timestamp: "2026-05-10T12:00:00Z",
  audit_hash: "sha256:0001abcd",
  input_ref: "blob://acme/in/01HQ",
  trace: [
    {
      rule_id: "rule.input.secret-scan",
      bundle_id: "bundle.acme.input",
      decision_type: DecisionType.deny,
      reason: "matched aws-access-key pattern",
      timestamp: "2026-05-10T12:00:00Z",
    },
  ],
};

describe("DecisionExpandRow (D8b — Trace + Input + Bundle context + Actions)", () => {
  it("renders all four sections (Trace, Input, Bundle context, Actions)", async () => {
    server.use(
      http.get("*/api/v1/artifacts/blob://acme/in/01HQ", () =>
        HttpResponse.json({ content_base64: btoa('{"prompt":"redacted"}') }),
      ),
      http.get("*/api/v1/artifacts/*", () =>
        HttpResponse.json({ content_base64: btoa('{"prompt":"redacted"}') }),
      ),
    );
    renderWithProviders(<DecisionExpandRow decision={baseDecision} />);
    // Scope to <h3> headings to avoid collisions with rule_ids that
    // happen to contain "input" or "trace" etc.
    expect(
      await screen.findByRole("heading", { name: /^trace$/i }),
    ).not.toBeNull();
    expect(
      await screen.findByRole("heading", { name: /^input$/i }),
    ).not.toBeNull();
    expect(
      await screen.findByRole("heading", { name: /bundle context/i }),
    ).not.toBeNull();
    expect(
      await screen.findByRole("heading", { name: /actions/i }),
    ).not.toBeNull();
  });

  it("Trace section lists each TraceStep with rule_id + decision_type + reason", async () => {
    renderWithProviders(<DecisionExpandRow decision={baseDecision} />);
    expect(
      await screen.findByText("rule.input.secret-scan"),
    ).not.toBeNull();
    expect(screen.getByText(/matched aws-access-key pattern/i)).not.toBeNull();
    // The trace step decision_type is rendered as a badge with the literal
    // type. Multiple "deny" badges are fine; assert at least one.
    expect(screen.getAllByText(/deny/i).length).toBeGreaterThan(0);
  });

  it("Trace section renders 'Awaiting Backend 3/4 backfill' placeholder when trace is empty", async () => {
    renderWithProviders(
      <DecisionExpandRow
        decision={{ ...baseDecision, trace: [] }}
      />,
    );
    expect(
      await screen.findByText(/awaiting backend 3\/4 backfill/i),
    ).not.toBeNull();
  });

  it("Bundle context renders bundle_id + bundle_version + audit_hash chip", async () => {
    renderWithProviders(<DecisionExpandRow decision={baseDecision} />);
    expect(
      await screen.findByText("bundle.acme.input"),
    ).not.toBeNull();
    expect(screen.getByText("v3")).not.toBeNull();
    // Audit hash chip is the inline CodeBlock showing the first 8 chars of
    // the sha256.
    const chip = screen.getByLabelText(/copy.*sha256:0001abcd/i);
    expect(chip).not.toBeNull();
    expect(chip.textContent).toContain("sha256:0");
  });

  // Replay + What-if button presence + handler coverage now lives in
  // DecisionExpandRow.replay.test.tsx (Replay) and WhatIfDrawer.test.tsx
  // (What-if drawer). The D8b stub assertions were intentionally removed
  // when D9b wired real handlers; data-stub="d9b" is no longer present.

  it("'Open rule' link wires to the D10a cross-link contract", async () => {
    renderWithProviders(<DecisionExpandRow decision={baseDecision} />);
    const link = await screen.findByRole("link", { name: /open rule/i });
    expect(link.getAttribute("href")).toBe(
      "/policies?rule=rule.input.secret-scan&open=editor",
    );
  });

  it("Input section degrades gracefully when input_ref is missing", async () => {
    renderWithProviders(
      <DecisionExpandRow
        decision={{ ...baseDecision, input_ref: undefined }}
      />,
    );
    // Both the header subtitle and the body placeholder mention "no
    // input ref recorded" -- assert via getAllByText to permit that.
    const matches = await screen.findAllByText(/no input ref recorded/i);
    expect(matches.length).toBeGreaterThan(0);
  });
});
