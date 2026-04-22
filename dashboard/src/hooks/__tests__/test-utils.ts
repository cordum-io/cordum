import React, { act } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRoot, type Root } from "react-dom/client";
import { vi } from "vitest";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

export function createTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
}

interface RenderWithQueryClientResult<T> {
  result: { current: T | undefined };
  queryClient: QueryClient;
  rerender: (nextHook?: () => T) => void;
  unmount: () => void;
  waitFor: (assertion: () => void, timeoutMs?: number) => Promise<void>;
}

export function renderWithQueryClient<T>(
  hook: () => T,
  queryClient = createTestQueryClient(),
): RenderWithQueryClientResult<T> {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root: Root = createRoot(container);
  const result = { current: undefined as T | undefined };
  let currentHook = hook;

  function HookHarness() {
    result.current = currentHook();
    return null;
  }

  function render() {
    root.render(
      React.createElement(
        QueryClientProvider,
        { client: queryClient },
        React.createElement(HookHarness),
      ),
    );
  }

  act(() => {
    render();
  });

  async function waitFor(assertion: () => void, timeoutMs = 2000): Promise<void> {
    const start = Date.now();
    while (true) {
      try {
        assertion();
        return;
      } catch (error) {
        if (Date.now() - start >= timeoutMs) {
          throw error;
        }
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 10));
        });
      }
    }
  }

  return {
    result,
    queryClient,
    rerender: (nextHook) => {
      if (nextHook) currentHook = nextHook;
      act(() => {
        render();
      });
    },
    unmount: () => {
      act(() => {
        root.unmount();
      });
      container.remove();
      queryClient.clear();
    },
    waitFor,
  };
}

type ResponseMatcher =
  | string
  | RegExp
  | ((url: string, init: RequestInit | undefined) => boolean);

interface MockFetchResponse {
  match: ResponseMatcher;
  method?: string;
  status?: number;
  body?: unknown;
  headers?: Record<string, string>;
  rejectWith?: Error;
}

function matchesResponse(
  response: MockFetchResponse,
  url: string,
  init: RequestInit | undefined,
): boolean {
  if (response.method && response.method.toUpperCase() !== (init?.method ?? "GET").toUpperCase()) {
    return false;
  }

  if (typeof response.match === "string") {
    return url.includes(response.match);
  }
  if (response.match instanceof RegExp) {
    return response.match.test(url);
  }
  return response.match(url, init);
}

export function mockFetch(responses: MockFetchResponse[]) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const url =
      typeof input === "string"
        ? input
        : input instanceof URL
          ? input.toString()
          : input.url;
    const matched = responses.find((response) => matchesResponse(response, url, init));

    if (!matched) {
      throw new Error(`No mock fetch response configured for ${init?.method ?? "GET"} ${url}`);
    }

    if (matched.rejectWith) {
      throw matched.rejectWith;
    }

    const status = matched.status ?? 200;
    const body = matched.body === undefined ? null : JSON.stringify(matched.body);
    const headers = {
      "Content-Type": "application/json",
      ...(matched.headers ?? {}),
    };
    return new Response(body, { status, headers });
  });
}
