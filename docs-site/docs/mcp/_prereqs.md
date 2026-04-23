<!-- Shared prereqs for quickstart-claude-code / quickstart-cursor /
     quickstart-vscode. Keep this file the single source of truth;
     update once, mirror into each quickstart with a short include
     reference. -->

## Prereqs

1. **Cordum running.** Local dev:

   ```bash
   make dev-up         # docker-compose up, all 7 services + dashboard
   ```

   or a real cluster with the gateway reachable at
   `https://cordum.yourco.internal:8081` (substitute your host).

2. **API key.** Cordum issues bearer-token API keys via
   `cordumctl auth key create`. Export it:

   ```bash
   export CORDUM_API_KEY="ck-…"
   export CORDUM_GATEWAY="https://localhost:8081"   # or your cluster host
   ```

3. **MCP endpoint.** The gateway's HTTP MCP route is:

   ```
   POST {CORDUM_GATEWAY}/mcp/message
   GET  {CORDUM_GATEWAY}/mcp/sse
   ```

   Authentication: pass the API key as `Authorization: Bearer ${CORDUM_API_KEY}`
   and the tenant via `X-Tenant-ID: ${CORDUM_TENANT_ID:-default}`.

4. **TLS.** Local dev uses a self-signed cert at
   `${CORDUM_TLS_DIR}/ca/ca.crt`; pin it with your client if available.
   For production, use a trusted CA-issued cert.

   Clients that support custom CA bundles take the `ca.crt` directly
   (e.g. VS Code's `http.proxyStrictSSL` + `NODE_EXTRA_CA_CERTS`).
   Clients without per-MCP-server cert pinning need the CA installed
   at the OS trust store — see the client-specific quickstart for the
   exact commands.

5. **Tenant.** Set `CORDUM_TENANT_ID` to the tenant you want the MCP
   session to scope to. The gateway rejects calls whose `X-Tenant-ID`
   header doesn't match the API key's authorized tenants:

   ```bash
   export CORDUM_TENANT_ID="default"   # or your tenant slug
   ```

   If unset, the client defaults to `default`. Multi-tenant
   operators should always set this explicitly.

6. **Rate limits.** The gateway applies a baseline rate limit on the
   MCP endpoints: 100 req/s per API key with a short burst allowance.
   Exceeding the limit returns HTTP 429 with a
   `Retry-After` header; MCP clients interpret this as a transient
   failure and back off. Increase the limit per-tenant via
   `cordumctl config set rate.mcp.rps <n>` if a legitimate workload
   hits it.

## First prompts to try

Once the client is connected, these three prompts exercise the
read-only surface and confirm end-to-end wiring:

1. *"List my recent jobs."* — picks `cordum_list_jobs`.
2. *"What policies are active?"* — picks `cordum_list_workflows` + /
   an output-rule query; follow-up reads a policy bundle via the
   resource URI.
3. *"Show me pending approvals."* — picks `cordum_list_pending_approvals`.
