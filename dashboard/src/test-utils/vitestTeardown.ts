// Vitest rejects every RPC call still pending when a worker shuts down with
// `EnvironmentTeardownError: [vitest-worker]: Closing rpc while "<method>" was pending`.
// This is the exact error for a console line still in flight (task-04eef2b3).
export const LOST_CONSOLE_LOG_AT_TEARDOWN =
  '[vitest-worker]: Closing rpc while "onUserConsoleLog" was pending';

export function isLostConsoleLogAtTeardown(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) return false;
  const { name, message } = error as { name?: unknown; message?: unknown };
  return (
    name === 'EnvironmentTeardownError' && message === LOST_CONSOLE_LOG_AT_TEARDOWN
  );
}
