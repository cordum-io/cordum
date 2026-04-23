import { useQueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { renderWithProviders } from "./render";

function QueryClientProbe() {
  const client = useQueryClient();
  return <div data-testid="query-client-present">{client ? "yes" : "no"}</div>;
}

describe("renderWithProviders", () => {
  it("mounts simple UI", () => {
    const { getByText } = renderWithProviders(<div>hi</div>);

    expect(getByText("hi").textContent).toBe("hi");
  });

  it("provides a QueryClient", () => {
    const { getByTestId } = renderWithProviders(<QueryClientProbe />);

    expect(getByTestId("query-client-present").textContent).toBe("yes");
  });
});
