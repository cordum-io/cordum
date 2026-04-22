# Wildcard Grading Reference

Every harness (Go, Python, TypeScript) implements the same five
wildcard tokens. The `parity/` directory's compare_verdicts runner
asserts they agree on every case in `parity/scenarios.json`.

## Tokens

### `$any$`

Matches any value: strings, numbers, booleans, objects, arrays, `null`.

Use when an opaque id or nonce appears in a response and the test
only cares that SOMETHING is there.

```json
{"bodyMatches": {"$.id": "$any$"}}
```

### `$timestamp$`

Matches a string that parses as RFC3339 / ISO-8601. Z and `+HH:MM`
offsets both accepted. Nanosecond precision accepted.

```json
{"bodyMatches": {"$.created_at": "$timestamp$"}}
```

Invalid examples that fail: `"not-a-date"`, `"2026-13-99"`, `42`.

### `$uuid$`

Matches the canonical UUID shape
`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`.
Version bit is NOT enforced — any valid-shape UUID matches.

```json
{"bodyMatches": {"$.request_id": "$uuid$"}}
```

### `$int$`

Matches an integer value. JSON numbers must round-trip to int (so
`42.0` passes but `3.14` fails). Booleans are REJECTED even in Python
where `True` is technically a subclass of `int` — every harness
carries an explicit guard.

```json
{"bodyMatches": {"$.version": "$int$"}}
```

### `$request_id$`

Alias for `$any$` today. Reserved so a future stricter form
(e.g. require the X-Request-Id header format) can land without
fixture churn.

## What IS NOT a wildcard

* Partial matches: `"prefix-$any$"` is a literal 15-character string.
* Non-string fields: `[$any$]` — the array element is the literal
  string `"$any$"`, NOT a wildcard.
* Unknown tokens: `$foo$`, `$bar$`. Every harness fails on unknown
  wildcards.

## Cross-harness parity

The canonical scenarios live in `parity/scenarios.json`. Adding a new
scenario there automatically exercises every harness's diff engine —
CI fails if any harness disagrees.

Run locally:

```sh
bash parity/run.sh
# → parity: all 21 scenarios agreed across go/python/typescript + matched want_pass
```
