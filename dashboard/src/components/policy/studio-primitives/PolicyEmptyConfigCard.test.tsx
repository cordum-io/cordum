import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import {
  __policyEmptyConfigCardInternal,
  PolicyEmptyConfigCard,
} from "./PolicyEmptyConfigCard";

describe("PolicyEmptyConfigCard", () => {
  it("renders CTA button only when callback and label exist", () => {
    const withCta = renderToStaticMarkup(
      <PolicyEmptyConfigCard
        title="No constraints"
        description="Add constraints to enforce limits."
        ctaLabel="Configure now"
        onCtaClick={() => {}}
      />,
    );
    const withoutCta = renderToStaticMarkup(
      <PolicyEmptyConfigCard
        title="No constraints"
        description="Add constraints to enforce limits."
      />,
    );

    expect(withCta).toContain("Configure now");
    expect(withoutCta).not.toContain("Configure now");
  });

  it("invokes CTA callback helper", () => {
    const onClick = vi.fn();
    __policyEmptyConfigCardInternal.invokeEmptyConfigCta(onClick);
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});
