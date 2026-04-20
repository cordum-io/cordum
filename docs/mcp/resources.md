# MCP Resources — The `cordum://` URI Scheme

Cordum exposes its records as MCP resources under a dedicated URI
scheme. An MCP client dereferences a URI to pull the underlying JSON
record; LLMs can thread URIs through conversations as stable
references (e.g. "see `cordum://runs/r-42/timeline`").

This page is a **public API surface**. Fields marked **stable** will
not be renamed or removed without a deprecation window; fields marked
**experimental** may change between releases.

---

## Scheme

```
cordum://<kind>/<id>[/<sub>]
cordum://audit/<tenant>/<seq>
```

* **scheme** — always `cordum` (lowercase).
* **kind** — one of `jobs`, `runs`, `workflows`, `packs`, `topics`,
  `agents`, `audit`. Exactly this set today; additions will be
  announced in CHANGELOG.md.
* **id** — the record identifier. For `topics/{name}` this is the
  topic name; for `audit/{tenant}/{seq}` the first segment is the
  tenant and the second is the sequence number.
* **sub** — optional sub-resource. Today only `runs/<id>/timeline`.

Invalid URIs return `ErrInvalidCordumURI`; unknown kinds return
`unknown kind`.

---

## Templates

Each template is registered on the MCP `resources/templates/list`
endpoint so clients discover it without hard-coding paths.

| Template                              | Record type    | Access |
| ------------------------------------- | -------------- | ------ |
| `cordum://jobs/{id}`                  | Job            | tenant |
| `cordum://runs/{id}`                  | Workflow run   | tenant |
| `cordum://runs/{id}/timeline`         | Run timeline   | tenant |
| `cordum://workflows/{id}`             | Workflow def   | tenant |
| `cordum://packs/{id}`                 | Installed pack | tenant |
| `cordum://topics/{name}`              | Topic          | public within tenant |
| `cordum://agents/{id}`                | Agent identity | tenant (admin for cross-tenant) |
| `cordum://audit/{tenant}/{seq}`       | Audit event    | admin only |

---

## Content shape

Every `resources/read` response returns a single JSON text item:

```json
{
  "uri": "cordum://runs/r-42/timeline",
  "mimeType": "application/json",
  "text": "{\"id\":\"r-42\",\"kind\":\"run_timeline\",\"data\":{...}}"
}
```

The embedded `text` decodes to:

```json
{
  "id": "<record id>",
  "kind": "<record kind>",
  "data": { /* full record from the gateway */ }
}
```

**`id` and `kind` are stable.** **`data`** passes through the gateway's
JSON body verbatim — individual fields track the gateway's existing
REST-API contract; see the
[OpenAPI spec](../../api/openapi.yaml) for per-field stability.

---

## URI stability policy

### Stable

* The **scheme** (`cordum`) will never change.
* The **kind segment** set (`jobs`, `runs`, `workflows`, `packs`,
  `topics`, `agents`, `audit`) will only grow — existing kinds will
  not be renamed or removed.
* The **id segment** semantics are stable: `{id}` matches the same
  identifier operators see in the dashboard and in REST responses.
* The **envelope** — `{id, kind, data}` — is stable.

### Experimental

* **`data` field shape** tracks the gateway REST API; additions are
  backward-compatible, but individual operators should pin an SDK or
  gateway version if they need guarantees on a specific field.
* **Sub-resources** other than `runs/{id}/timeline` are experimental
  until promoted. A new sub-resource will be announced in the
  CHANGELOG at least one release before it becomes stable.

### Deprecation window

Any removal or rename of a stable element:

1. Ships as a `@deprecated` annotation in the resource template
   description.
2. Continues to function for **at least one minor release**.
3. Is removed only after a major version bump, with a redirect
   template (e.g. `cordum://{old}/{id}` → `cordum://{new}/{id}`) where
   practical.

---

## Examples

### Dereference a run timeline

```jsonrpc
→ {"jsonrpc":"2.0","id":1,"method":"resources/read",
    "params":{"uri":"cordum://runs/r-42/timeline"}}

← {"jsonrpc":"2.0","id":1,"result":{
    "contents":[{
      "uri":"cordum://runs/r-42/timeline",
      "mimeType":"application/json",
      "text":"{\"id\":\"r-42\",\"kind\":\"run_timeline\",\"data\":{\"events\":[...]}}"
    }]
  }}
```

### Dereference an audit event (admin)

```jsonrpc
→ {"jsonrpc":"2.0","id":2,"method":"resources/read",
    "params":{"uri":"cordum://audit/tenant-1/42"}}

← {"jsonrpc":"2.0","id":2,"result":{"contents":[{
    "uri":"cordum://audit/tenant-1/42",
    "mimeType":"application/json",
    "text":"{\"id\":\"tenant-1/42\",\"kind\":\"audit_event\",\"data\":{\"seq\":42,\"event_hash\":\"ab…\"}}"
  }]}}
```

---

## Pagination

Lists and bulk audit queries are paginated — see
[docs/mcp/tools.md#pagination-envelope](./tools.md#pagination-envelope).
Cursors are opaque base64 strings; clients must pass them back verbatim.

---

## Related

* [docs/mcp/tools.md](./tools.md) — tool catalogue.
* [docs/mcp/quickstart-claude-code.md](./quickstart-claude-code.md)
* [docs/mcp/quickstart-cursor.md](./quickstart-cursor.md)
* [docs/mcp/quickstart-vscode.md](./quickstart-vscode.md)
