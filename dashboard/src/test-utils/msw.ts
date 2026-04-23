import { setupServer } from "msw/node";
import { http, HttpResponse } from "msw";
import { baseHandlers } from "./handlers";

export { http, HttpResponse };

export const server = setupServer(...baseHandlers);

let listening = false;

export function ensureMswServerListening(): void {
  if (listening) return;

  server.listen({ onUnhandledRequest: "warn" });
  listening = true;
}

export function resetMswServer(): void {
  if (!listening) return;
  server.resetHandlers();
}

export function closeMswServer(): void {
  if (!listening) return;
  server.close();
  listening = false;
}

export function isMswServerListening(): boolean {
  return listening;
}
