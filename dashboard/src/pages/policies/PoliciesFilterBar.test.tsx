import { describe, expect, it, vi } from "vitest";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test-utils/render";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import { PoliciesFilterBar } from "./PoliciesFilterBar";

describe("PoliciesFilterBar", () => {
  it("roundtrips type from the URL and notifies the parent filter payload", async () => {
    const onFiltersChange = vi.fn();

    renderWithProviders(
      <NuqsTestingAdapter searchParams="?type=input">
        <PoliciesFilterBar onFiltersChange={onFiltersChange} />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies?type=input"] },
    );

    const typeFilter = await screen.findByLabelText<HTMLSelectElement>(
      /filter rules by type/i,
    );
    expect(typeFilter.value).toBe(RuleType.input);
    await waitFor(() => {
      expect(onFiltersChange).toHaveBeenLastCalledWith({
        type: RuleType.input,
      });
    });
  });

  it("updates URL-backed filter controls for status scope and search", async () => {
    const onFiltersChange = vi.fn();

    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <PoliciesFilterBar onFiltersChange={onFiltersChange} />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies"] },
    );

    fireEvent.change(await screen.findByLabelText(/filter rules by status/i), {
      target: { value: RuleStatus.published },
    });
    fireEvent.change(screen.getByLabelText(/filter rules by scope/i), {
      target: { value: "tenant:acme" },
    });
    fireEvent.change(screen.getByLabelText(/search rules/i), {
      target: { value: "pii" },
    });

    await waitFor(() => {
      expect(onFiltersChange).toHaveBeenLastCalledWith({
        status: RuleStatus.published,
        scope: "tenant:acme",
        search: "pii",
      });
    });
  });
});
