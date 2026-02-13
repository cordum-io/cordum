import { afterEach, describe, expect, it, vi } from "vitest";

async function loadLoggerWithLevel(level?: string) {
  vi.resetModules();
  if (level === undefined) {
    vi.unstubAllEnvs();
  } else {
    vi.stubEnv("VITE_LOG_LEVEL", level);
  }
  return await import("./logger");
}

describe("logger", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllEnvs();
  });

  it("respects warn threshold (suppresses debug/info)", async () => {
    const logSpy = vi.spyOn(console, "log").mockImplementation(() => {});
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});

    const { logger } = await loadLoggerWithLevel("warn");
    logger.debug("comp", "debug");
    logger.info("comp", "info");
    logger.warn("comp", "warn");

    expect(logSpy).not.toHaveBeenCalled();
    expect(warnSpy).toHaveBeenCalledTimes(1);
  });

  it("respects error threshold (warn suppressed)", async () => {
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});

    const { logger } = await loadLoggerWithLevel("error");
    logger.warn("comp", "warn");
    logger.error("comp", "error");

    expect(warnSpy).not.toHaveBeenCalled();
    expect(errorSpy).toHaveBeenCalledTimes(1);
  });

  it("logs fields when provided", async () => {
    const logSpy = vi.spyOn(console, "log").mockImplementation(() => {});
    const { logger } = await loadLoggerWithLevel("debug");

    logger.info("comp", "message", { id: 1 });
    expect(logSpy).toHaveBeenCalled();
  });
});
