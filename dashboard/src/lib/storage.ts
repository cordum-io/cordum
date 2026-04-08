import { logger } from "./logger";

const STORAGE_COMPONENT = "storage";

function storageErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message.trim()) {
    return error.message;
  }
  return String(error);
}

function resolveLocalStorage(): Storage | null {
  if (typeof window === "undefined") {
    return null;
  }
  try {
    return window.localStorage;
  } catch (error) {
    logger.warn(STORAGE_COMPONENT, "localStorage unavailable", {
      error: storageErrorMessage(error),
    });
    return null;
  }
}

export const safeLocalStorage = {
  getItem(key: string): string | null {
    const storage = resolveLocalStorage();
    if (!storage) {
      return null;
    }
    try {
      return storage.getItem(key);
    } catch (error) {
      logger.warn(STORAGE_COMPONENT, "localStorage getItem failed", {
        key,
        error: storageErrorMessage(error),
      });
      return null;
    }
  },
  setItem(key: string, value: string): boolean {
    const storage = resolveLocalStorage();
    if (!storage) {
      return false;
    }
    try {
      storage.setItem(key, value);
      return true;
    } catch (error) {
      logger.warn(STORAGE_COMPONENT, "localStorage setItem failed", {
        key,
        error: storageErrorMessage(error),
      });
      return false;
    }
  },
  removeItem(key: string): boolean {
    const storage = resolveLocalStorage();
    if (!storage) {
      return false;
    }
    try {
      storage.removeItem(key);
      return true;
    } catch (error) {
      logger.warn(STORAGE_COMPONENT, "localStorage removeItem failed", {
        key,
        error: storageErrorMessage(error),
      });
      return false;
    }
  },
};
