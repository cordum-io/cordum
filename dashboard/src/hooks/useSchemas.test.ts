import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import {
  __schemasInternal,
  useDeleteSchema,
  useRegisterSchema,
  useSchema,
  useSchemas,
} from "./useSchemas";

const { addToastMock, loggerMock } = vi.hoisted(() => ({
  addToastMock: vi.fn(),
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

const { mockConfigState } = vi.hoisted(() => ({
  mockConfigState: {
    apiBaseUrl: "/api/v1",
    apiKey: "",
    tenantId: "",
    principalId: "",
    principalRole: "",
    user: null,
    logout: vi.fn(),
  },
}));

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../state/toast", () => ({
  useToastStore: {
    getState: () => ({ addToast: addToastMock }),
  },
}));

vi.mock("../lib/logger", () => ({
  logger: loggerMock,
}));

describe("useSchemas internals", () => {
  it("parseJsonSchemaFields parses properties and required fields", () => {
    const fields = __schemasInternal.parseJsonSchemaFields({
      type: "object",
      required: ["id"],
      properties: {
        id: { type: "string", description: "Identifier" },
        count: { type: "number" },
      },
    });

    expect(fields).toEqual([
      { name: "id", type: "string", required: true, description: "Identifier" },
      { name: "count", type: "number", required: false },
    ]);
  });

  it("parseJsonSchemaFields returns empty for non-object/missing properties", () => {
    expect(__schemasInternal.parseJsonSchemaFields({})).toEqual([]);
    expect(__schemasInternal.parseJsonSchemaFields(null as unknown as Record<string, unknown>)).toEqual([]);
  });

  it("parseJsonSchemaFields supports direct property maps as schema", () => {
    const fields = __schemasInternal.parseJsonSchemaFields({
      fieldA: { type: "string", description: "Field A" },
      fieldB: { type: "boolean" },
    });

    expect(fields).toEqual([
      { name: "fieldA", type: "string", required: false, description: "Field A" },
      { name: "fieldB", type: "boolean", required: false },
    ]);
  });
});

describe("useSchemas hooks", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.clearAllMocks();
    mockConfigState.apiBaseUrl = "/api/v1";
    mockConfigState.apiKey = "";
    mockConfigState.tenantId = "";
    mockConfigState.principalId = "";
    mockConfigState.principalRole = "";
    mockConfigState.user = null;
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000123");
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("useSchemas starts in loading state", () => {
    mockFetch([{ match: "/schemas", method: "GET", body: [] }]);
    const hook = renderWithQueryClient(() => useSchemas());
    expect(hook.result.current?.isLoading).toBe(true);
    expect(hook.result.current?.data).toBeUndefined();
    hook.unmount();
  });

  it("useSchemas returns error state on fetch failure", async () => {
    mockFetch([{ match: "/schemas", method: "GET", status: 500, body: { error: "server error" } }]);
    const hook = renderWithQueryClient(() => useSchemas());
    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    });
    hook.unmount();
  });

  it("useSchema returns error state on fetch failure", async () => {
    mockFetch([{ match: "/schemas/s1", method: "GET", status: 404, body: { error: "not found" } }]);
    const hook = renderWithQueryClient(() => useSchema("s1"));
    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    });
    hook.unmount();
  });

  it("useSchemas fetches schema ids and maps to Schema objects", async () => {
    mockFetch([
      {
        match: "/schemas",
        method: "GET",
        body: { schemas: ["schema-a", "schema-b"] },
      },
    ]);

    const hook = renderWithQueryClient(() => useSchemas());
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data?.items).toEqual([
      { id: "schema-a", name: "schema-a", fields: [] },
      { id: "schema-b", name: "schema-b", fields: [] },
    ]);
    hook.unmount();
  });

  it("useSchema fetches schema detail and parses fields", async () => {
    mockFetch([
      {
        match: "/schemas/schema-a",
        method: "GET",
        body: {
          id: "schema-a",
          schema: {
            type: "object",
            required: ["id"],
            properties: {
              id: { type: "string", description: "Identifier" },
              score: { type: "number" },
            },
          },
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useSchema("schema-a"));
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data).toMatchObject({
      id: "schema-a",
      name: "schema-a",
      fields: [
        { name: "id", type: "string", required: true, description: "Identifier" },
        { name: "score", type: "number", required: false },
      ],
    });
    hook.unmount();
  });

  it("useRegisterSchema posts payload and invalidates schemas cache", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/schemas",
        method: "POST",
        body: null,
      },
    ]);

    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const hook = renderWithQueryClient(() => useRegisterSchema(), queryClient);

    await act(async () => {
      await hook.result.current?.mutateAsync({
        id: "schema-new",
        schema: { type: "object", properties: { id: { type: "string" } } },
      });
    });

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({
      id: "schema-new",
      schema: { type: "object", properties: { id: { type: "string" } } },
    });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["schemas"] });
    expect(addToastMock).toHaveBeenCalledWith({ type: "success", title: "Schema registered" });

    hook.unmount();
  });

  it("useDeleteSchema deletes and invalidates schemas cache", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/schemas/schema-a",
        method: "DELETE",
        body: null,
      },
    ]);

    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const hook = renderWithQueryClient(() => useDeleteSchema(), queryClient);

    await act(async () => {
      await hook.result.current?.mutateAsync("schema-a");
    });

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["schemas"] });
    expect(addToastMock).toHaveBeenCalledWith({ type: "success", title: "Schema deleted" });

    hook.unmount();
  });
});

