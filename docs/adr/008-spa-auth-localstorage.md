# ADR-008: SPA Authentication — localStorage API Key Storage

- Status: Accepted
- Date: 2026-03-02

## Context

The Cordum dashboard is a single-page application (SPA) that authenticates API requests using an API key sent via the `X-Api-Key` header. The key must be accessible to JavaScript for every fetch call. The question is whether localStorage is an acceptable storage mechanism or whether we should migrate to httpOnly cookies with server-side sessions.

## Decision

**Keep localStorage storage for API keys.** The risk is acceptable given current mitigations.

### Storage Mechanism

- API key stored under `cordum-api-key` in `window.localStorage` (`state/config.ts`)
- User profile stored under `cordum-user` (id, tenant, roles)
- Login timestamp stored under `cordum-login-ts`
- Cross-tab sync via `BroadcastChannel` for login/logout propagation

### Why Not httpOnly Cookies

httpOnly cookies prevent JavaScript access, which protects against XSS token theft. However:

1. **JS needs the token**: Every API call uses `X-Api-Key` header via the fetch client (`api/client.ts`). With httpOnly cookies, we'd need a server-side proxy to inject credentials, adding latency and infrastructure complexity.
2. **No server-side session infrastructure**: The API gateway is stateless — it validates API keys directly against the key store. Adding session management would require a session store, CSRF protection, and cookie domain configuration.
3. **CSRF trade-off**: Cookies introduce CSRF risk that must be mitigated with tokens. The current header-based auth is inherently CSRF-immune.

## Mitigations

The following controls reduce the XSS-based token theft risk to acceptable levels:

| Control | Evidence |
|---------|----------|
| No `dangerouslySetInnerHTML` | Grep verified: zero occurrences across all dashboard source |
| No `eval()` / `new Function()` | Grep verified: zero occurrences |
| No `.innerHTML` assignment | Grep verified: zero occurrences |
| CSP: `default-src 'none'` | `middleware.go` — blocks all inline scripts, external scripts, and plugin content |
| `frame-ancestors 'none'` | Prevents clickjacking and framing attacks |
| `X-Frame-Options: DENY` | Defense-in-depth against framing |
| `X-Content-Type-Options: nosniff` | Prevents MIME-type confusion attacks |
| `Strict-Transport-Security` | HSTS in production with `includeSubDomains` |
| No third-party scripts | No analytics, ads, or external script tags in `index.html` |
| Tenant isolation | `X-Tenant-ID` header validated server-side; tenant lock prevents mutation after login |

## Revisit Conditions

Migrate to httpOnly cookies or a BFF (Backend-for-Frontend) proxy if any of these change:

1. **Third-party scripts are loaded** (analytics, chat widgets, CDN-hosted libraries)
2. **CSP is relaxed** (`unsafe-inline`, `unsafe-eval`, or wildcard `script-src`)
3. **User-generated content is rendered** without sanitization
4. **The dashboard becomes multi-tenant** where one tenant's admin could inject scripts visible to another tenant
5. **Compliance requirements** mandate httpOnly cookie storage (SOC 2 Type II, FedRAMP)

## Consequences

- API keys remain accessible to any JavaScript running in the page context
- If a novel XSS vector is discovered, token theft is possible until the vector is patched
- Cross-tab logout works immediately via BroadcastChannel (no cookie expiry delay)
- No CSRF protection needed (header-based auth is CSRF-immune)
- No additional server infrastructure required for session management
