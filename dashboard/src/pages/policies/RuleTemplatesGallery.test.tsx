import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { RULE_TEMPLATES } from "@/lib/policy-studio/templates";
import { RuleTemplatesGallery } from "./RuleTemplatesGallery";

describe("RuleTemplatesGallery", () => {
  it("renders the disclosure summary collapsed by default with the template count", () => {
    const onInsert = vi.fn();
    const { container } = render(
      <RuleTemplatesGallery onInsert={onInsert} />,
    );
    const details = container.querySelector("details");
    expect(details).not.toBeNull();
    expect(details?.open).toBe(false);
    // Count badge — renders 7 once template files exist.
    expect(screen.getByText(String(RULE_TEMPLATES.length))).not.toBeNull();
  });

  it("renders all seven template labels and descriptions when expanded", () => {
    const onInsert = vi.fn();
    const { container } = render(
      <RuleTemplatesGallery onInsert={onInsert} />,
    );
    const details = container.querySelector("details")!;
    details.open = true;
    details.dispatchEvent(new Event("toggle"));

    // The seven canonical templates.
    const expected = [
      "PII redact",
      "Secret scan",
      "Rate limit",
      "Approval gate",
      "Edge tool allowlist",
      "Edge file access guard",
      "Edge prompt classifier",
    ];
    for (const label of expected) {
      expect(screen.getByText(label)).not.toBeNull();
    }
    // Spot-check that the description renders alongside, not just the label.
    expect(
      screen.getByText(/Redact PII \(emails, phone, SSN\)/),
    ).not.toBeNull();
    expect(
      screen.getByText(/Hard-deny input carrying API keys/),
    ).not.toBeNull();
  });

  it("calls onInsert with the matching template when a template button is clicked", () => {
    const onInsert = vi.fn();
    const { container } = render(
      <RuleTemplatesGallery onInsert={onInsert} />,
    );
    const details = container.querySelector("details")!;
    details.open = true;
    details.dispatchEvent(new Event("toggle"));

    const piiBtn = container.querySelector(
      '[data-template-id="pii-redact"]',
    ) as HTMLButtonElement;
    expect(piiBtn).not.toBeNull();
    fireEvent.click(piiBtn);

    expect(onInsert).toHaveBeenCalledTimes(1);
    const inserted = onInsert.mock.calls[0]?.[0];
    expect(inserted?.id).toBe("pii-redact");
    expect(inserted?.yaml).toMatch(/Template: PII Redact/);
    expect(inserted?.yaml).toMatch(/type: input/);
  });

  it("groups templates by rule type so authors can scan-pick by surface", () => {
    const onInsert = vi.fn();
    const { container } = render(
      <RuleTemplatesGallery onInsert={onInsert} />,
    );
    const details = container.querySelector("details")!;
    details.open = true;
    details.dispatchEvent(new Event("toggle"));

    // Group headers (Input / Velocity / Edge — the four templates' types).
    expect(screen.getByText("Input")).not.toBeNull();
    expect(screen.getByText("Velocity")).not.toBeNull();
    expect(screen.getByText("Edge")).not.toBeNull();
  });
});
