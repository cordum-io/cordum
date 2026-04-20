import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";

const { getMock, postMock } = vi.hoisted(() => ({
  getMock: vi.fn(),
  postMock: vi.fn(),
}));

vi.mock("../api/client", () => ({
  get: getMock,
  post: postMock,
}));

describe("lib api helpers", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("getSession calls /auth/session", async () => {
    getMock.mockResolvedValue({
      user: {
        id: "u1",
        username: "alice",
        email: "alice@example.com",
        display_name: "Alice",
        roles: ["admin"],
        tenant: "tenant-1",
      },
    });

    const result = await api.getSession();

    expect(getMock).toHaveBeenCalledWith("/auth/session");
    expect(result.user.id).toBe("u1");
  });

  it("logout posts to /auth/logout", async () => {
    postMock.mockResolvedValue(undefined);

    await api.logout();

    expect(postMock).toHaveBeenCalledWith("/auth/logout");
  });

  it("listApprovals maps backend items and filters null approvals", async () => {
    getMock.mockResolvedValue({
      items: [
        {
          approval_ref: "a1",
          job: {
            id: "j1",
            state: "RUNNING",
            topic: "sys.job.submit",
            updated_at: 1_707_000_000_000_000,
          },
        },
        {},
      ],
      next_cursor: 7,
    });

    const result = await api.listApprovals(50);

    expect(getMock).toHaveBeenCalledWith("/approvals?limit=50");
    expect(result.items).toHaveLength(1);
    expect(result.items[0].id).toBe("a1");
    expect(result.next_cursor).toBe(7);
  });

  it("listDLQPage maps backend DLQ entries", async () => {
    getMock.mockResolvedValue({
      items: [{ job_id: "j1", reason: "failed", topic: "sys.job.submit", attempts: 2 }],
    });

    const result = await api.listDLQPage(25);

    expect(getMock).toHaveBeenCalledWith("/dlq/page?limit=25");
    expect(result.items).toEqual([
      expect.objectContaining({
        id: "j1",
        jobId: "j1",
        reason: "failed",
        retryCount: 2,
      }),
    ]);
  });
});
