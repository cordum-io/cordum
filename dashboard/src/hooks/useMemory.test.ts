import { describe, expect, it } from "vitest";
import { __memoryInternal } from "./useMemory";

const {
  mapMemoryEntries,
  attachMemoryEntries,
  mapJobArtifactsResponse,
  fallbackArtifactsFromJobDetail,
  dedupeArtifacts,
} = __memoryInternal;

describe("mapMemoryEntries", () => {
  it("maps array of record entries", () => {
    const raw = [
      { role: "user", content: "Hello", id: "m1", timestamp: "2026-01-01T00:00:00Z" },
      { role: "assistant", content: "Hi there", id: "m2" },
    ];
    const entries = mapMemoryEntries(raw);
    expect(entries).toHaveLength(2);
    expect(entries[0]).toMatchObject({ id: "m1", role: "user", content: "Hello" });
    expect(entries[1]).toMatchObject({ id: "m2", role: "assistant", content: "Hi there" });
  });

  it("maps array of plain strings", () => {
    const raw = ["first line", "second line", ""];
    const entries = mapMemoryEntries(raw);
    expect(entries).toHaveLength(2);
    expect(entries[0]).toMatchObject({ role: "system", content: "first line" });
    expect(entries[1]).toMatchObject({ role: "system", content: "second line" });
  });

  it("maps object with messages key", () => {
    const raw = {
      messages: [
        { role: "system", content: "You are helpful" },
        { role: "user", content: "Summarize this" },
      ],
    };
    const entries = mapMemoryEntries(raw);
    expect(entries).toHaveLength(2);
    expect(entries[0].role).toBe("system");
    expect(entries[1].role).toBe("user");
  });

  it("maps object with items key", () => {
    const raw = {
      items: [
        { role: "tool", content: "tool output" },
      ],
    };
    const entries = mapMemoryEntries(raw);
    expect(entries).toHaveLength(1);
    expect(entries[0].role).toBe("tool");
  });

  it("maps flat object keys using role guessing", () => {
    const raw = {
      prompt: "What is 2+2?",
      result: "4",
    };
    const entries = mapMemoryEntries(raw);
    expect(entries).toHaveLength(2);
    expect(entries[0]).toMatchObject({ role: "user", content: "What is 2+2?" });
    expect(entries[1]).toMatchObject({ role: "assistant", content: "4" });
  });

  it("normalizes unknown roles to unknown", () => {
    const raw = [{ role: "banana", content: "test" }];
    const entries = mapMemoryEntries(raw);
    expect(entries[0].role).toBe("unknown");
  });

  it("skips entries with empty content", () => {
    const raw = [
      { role: "user", content: "" },
      { role: "user", content: "valid" },
    ];
    const entries = mapMemoryEntries(raw);
    expect(entries).toHaveLength(1);
    expect(entries[0].content).toBe("valid");
  });

  it("returns empty array for null/undefined", () => {
    expect(mapMemoryEntries(null)).toEqual([]);
    expect(mapMemoryEntries(undefined)).toEqual([]);
  });

  it("generates stable IDs when id missing", () => {
    const raw = [{ role: "user", content: "no id" }];
    const entries = mapMemoryEntries(raw);
    expect(entries[0].id).toBe("entry-1");
  });

  it("parses timestamp from created_at", () => {
    const raw = [{ role: "user", content: "hi", created_at: "2026-02-01T10:00:00Z" }];
    const entries = mapMemoryEntries(raw);
    expect(entries[0].timestamp).toBe("2026-02-01T10:00:00Z");
  });
});

describe("attachMemoryEntries", () => {
  const base = { pointer: "p", key: "k", kind: "memory", size_bytes: 0, base64: "" } as const;

  it("returns payload as-is if entries already populated", () => {
    const payload = {
      ...base,
      entries: [{ id: "e1", role: "user" as const, content: "hello" }],
    };
    const result = attachMemoryEntries(payload);
    expect(result.entries).toBe(payload.entries);
  });

  it("derives entries from json when entries are empty", () => {
    const payload = {
      ...base,
      entries: [] as never[],
      json: [{ role: "user", content: "from json" }],
    };
    const result = attachMemoryEntries(payload);
    expect(result.entries!).toHaveLength(1);
    expect(result.entries![0].content).toBe("from json");
  });
});

describe("mapJobArtifactsResponse", () => {
  it("maps array of artifact objects", () => {
    const raw = [
      { ptr: "res:job:1", content_type: "application/json", size_bytes: 100 },
      { ptr: "res:job:2" },
    ];
    const refs = mapJobArtifactsResponse(raw);
    expect(refs).toHaveLength(2);
    expect(refs[0]).toMatchObject({ ptr: "res:job:1", contentType: "application/json", sizeBytes: 100 });
    expect(refs[1]).toMatchObject({ ptr: "res:job:2" });
  });

  it("maps object with items key", () => {
    const raw = { items: [{ ptr: "res:1" }] };
    const refs = mapJobArtifactsResponse(raw);
    expect(refs).toHaveLength(1);
    expect(refs[0].ptr).toBe("res:1");
  });

  it("maps object with artifacts key", () => {
    const raw = { artifacts: [{ pointer: "res:2" }] };
    const refs = mapJobArtifactsResponse(raw);
    expect(refs).toHaveLength(1);
    expect(refs[0].ptr).toBe("res:2");
  });

  it("maps array of plain strings", () => {
    const raw = ["ptr:abc", "ptr:def", ""];
    const refs = mapJobArtifactsResponse(raw);
    expect(refs).toHaveLength(2);
    expect(refs[0].ptr).toBe("ptr:abc");
  });

  it("returns empty for null", () => {
    expect(mapJobArtifactsResponse(null)).toEqual([]);
  });

  it("skips items with no ptr", () => {
    const raw = [{ content_type: "text/plain" }];
    const refs = mapJobArtifactsResponse(raw);
    expect(refs).toEqual([]);
  });
});

describe("fallbackArtifactsFromJobDetail", () => {
  it("extracts result_ptr from job detail", () => {
    const raw = { result_ptr: "redis://res:job-1", updated_at: "2026-01-01T00:00:00Z" };
    const refs = fallbackArtifactsFromJobDetail(raw);
    expect(refs).toHaveLength(1);
    expect(refs[0]).toMatchObject({ ptr: "redis://res:job-1", source: "result" });
  });

  it("extracts output_safety pointers", () => {
    const raw = {
      result_ptr: "res:main",
      output_safety: {
        original_ptr: "res:original",
        redacted_ptr: "res:redacted",
      },
    };
    const refs = fallbackArtifactsFromJobDetail(raw);
    expect(refs).toHaveLength(3);
    expect(refs.map((r) => r.ptr)).toEqual(["res:main", "res:original", "res:redacted"]);
  });

  it("deduplicates pointers", () => {
    const raw = {
      result_ptr: "res:same",
      output_safety: { original_ptr: "res:same" },
    };
    const refs = fallbackArtifactsFromJobDetail(raw);
    expect(refs).toHaveLength(1);
  });

  it("returns empty for null", () => {
    expect(fallbackArtifactsFromJobDetail(null)).toEqual([]);
  });
});

describe("dedupeArtifacts", () => {
  it("removes duplicate pointers", () => {
    const items = [
      { ptr: "a", source: "first" },
      { ptr: "b", source: "x" },
      { ptr: "a", source: "second" },
    ];
    const result = dedupeArtifacts(items);
    expect(result).toHaveLength(2);
    expect(result[0].source).toBe("first");
  });

  it("skips empty pointers", () => {
    const items = [{ ptr: "", source: "bad" }, { ptr: "ok", source: "good" }];
    const result = dedupeArtifacts(items);
    expect(result).toHaveLength(1);
    expect(result[0].ptr).toBe("ok");
  });
});
