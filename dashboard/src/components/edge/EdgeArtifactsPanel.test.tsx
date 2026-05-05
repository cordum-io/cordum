import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentActionEvent, EdgeArtifactPointer, EdgeSessionExportBundle } from "@/api/types";
import { fireEvent, renderWithProviders, screen } from "@/test-utils/render";
import { useExportEdgeSession } from "@/hooks/useEdgeSessions";
import { EdgeArtifactsPanel } from "./EdgeArtifactsPanel";

vi.mock("@/hooks/useEdgeSessions", () => ({
  useExportEdgeSession: vi.fn(),
}));

const exportMutate = vi.fn();

function makeArtifact(overrides: Partial<EdgeArtifactPointer> = {}): EdgeArtifactPointer {
  return {
    artifactType: "edge.diff",
    sessionId: "edge_sess_1",
    executionId: "edge_exec_1",
    eventId: "edge_evt_1",
    tenantId: "tenant-a",
    retentionClass: "audit",
    redactionLevel: "strict",
    sha256: "sha256:diff-1",
    uri: "edge-artifact://diff-1",
    createdAt: "2026-05-02T16:01:00Z",
    sizeBytes: 65_536,
    contentType: "application/json",
    ...overrides,
  };
}

const eventWithArtifact: AgentActionEvent = {
  eventId: "edge_evt_1",
  sessionId: "edge_sess_1",
  executionId: "edge_exec_1",
  tenantId: "tenant-a",
  seq: 1,
  ts: "2026-05-02T16:01:00Z",
  layer: "post_tool_use",
  kind: "artifact",
  inputHash: "hash-input",
  decision: "allow",
  status: "recorded",
  artifactPtrs: [makeArtifact()],
};

function makeExportBundle(): EdgeSessionExportBundle {
  return {
    manifestVersion: "edge.export.v1",
    generatedAt: "2026-05-02T16:02:00Z",
    tenantId: "tenant-a",
    session: {
      sessionId: "edge_sess_1",
      tenantId: "tenant-a",
      principalType: "human",
      mode: "local-dev",
      traceId: "trace-1",
      policyMode: "enforce",
      status: "ended",
      riskSummary: { deniedCount: 0, approvalCount: 1, artifactCount: 2 },
      startedAt: "2026-05-02T16:00:00Z",
    },
    artifacts: [makeArtifact({ sha256: "sha256:bundle", uri: "/api/v1/artifacts/bundle" })],
  };
}

function mockExport(data?: EdgeSessionExportBundle, error: Error | null = null) {
  vi.mocked(useExportEdgeSession).mockReturnValue({
    mutate: exportMutate,
    isPending: false,
    data,
    error,
  } as unknown as ReturnType<typeof useExportEdgeSession>);
}

describe("EdgeArtifactsPanel", () => {
  beforeEach(() => {
    exportMutate.mockReset();
    vi.mocked(useExportEdgeSession).mockReset();
  });

  it("renders artifact pointer metadata without raw bytes", () => {
    mockExport();

    renderWithProviders(<EdgeArtifactsPanel sessionId="edge_sess_1" events={[eventWithArtifact]} />);

    expect(screen.queryByText("diff")).not.toBeNull();
    expect(screen.queryByText("strict")).not.toBeNull();
    expect(screen.queryByText("audit")).not.toBeNull();
    expect(screen.queryByText("sha256:diff-1")).not.toBeNull();
    expect(screen.queryByText("64 KB")).not.toBeNull();
    expect(screen.queryByText("edge_evt_1")).not.toBeNull();
    expect(screen.queryByText(/payload body|command output|transcript text/i)).toBeNull();
  });

  it("triggers evidence export and shows exported artifact links", () => {
    mockExport(makeExportBundle());

    renderWithProviders(<EdgeArtifactsPanel sessionId="edge_sess_1" events={[eventWithArtifact]} />);

    fireEvent.click(screen.getByRole("button", { name: /Export evidence/i }));
    expect(exportMutate.mock.calls[0]?.[0]).toEqual({
      sessionId: "edge_sess_1",
      request: { maxEvents: 500 },
    });
    expect(screen.queryByText(/Export ready: edge.export.v1/i)).not.toBeNull();
    const link = screen.getByRole("link", { name: /Download/i }) as HTMLAnchorElement;
    expect(link.getAttribute("href")).toBe("/api/v1/artifacts/bundle");
  });

  it("uses callback views for opaque Edge artifact pointers and never links external URLs", () => {
    mockExport();
    const onViewArtifact = vi.fn();
    const external = makeArtifact({ sha256: "sha256:external", uri: "https://signed.example/download" });

    renderWithProviders(
      <EdgeArtifactsPanel
        sessionId="edge_sess_1"
        artifacts={[external]}
        onViewArtifact={onViewArtifact}
      />,
    );

    expect(screen.queryByRole("link")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /Pointer only/i }));
    expect(onViewArtifact.mock.calls[0]?.[0]).toMatchObject({ sha256: "sha256:external" });
  });
});
