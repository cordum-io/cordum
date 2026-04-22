import createClient, { type ClientOptions, type Client } from "openapi-fetch";
import type { components, operations, paths } from "./schema.js";

export type { components, operations, paths };
export { createClient };
export type CordumFetchClient = Client<paths>;

export function createGeneratedClient(clientOptions?: ClientOptions): CordumFetchClient {
  return createClient<paths>(clientOptions);
}
