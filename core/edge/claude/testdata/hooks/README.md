# Claude hook fixture corpus

These JSON files are inputs for the EDGE-016 mapper unit tests. They cover the
Claude Code hook events Cordum supports plus a few intentionally malformed or
version-drift shapes to prove the mapper degrades safely.

## Hard rules

- **Synthetic data only.** No real Claude transcripts, no real session IDs,
  no real `cwd`/file paths from a developer's machine, no real prompts, no
  real tool output, no API tokens, no OAuth secrets, no SSH keys, no emails,
  no IP addresses that resolve to anything. If you want to capture a live
  payload to write a regression, redact it first by hand and confirm with
  `grep` that no obvious secret marker (`Bearer `, `sk-`, `ghp_`, `AKIA`,
  long base64 blobs, etc.) survived before committing.
- **No raw Claude payloads on disk.** The runner keeps `RawPayload []byte`
  in memory only (json:`-`). If you need to add a fixture for a new shape,
  write it from scratch using the placeholder values in the existing files;
  do not copy/paste from a real run.
- **Stable, hashable.** Each file is a single JSON object. The mapper's
  input-hash regression depends on byte-stable fixtures, so do not reformat
  with editors that change ordering or whitespace policy. `gofmt` / IDE
  formatters never touch these files; treat them as data.
- **Naming.** `<event>_<tool>_<variant>.json` for the happy-path corpus,
  `<event>_<reason>.json` for degraded/malformed cases.

## Synthetic placeholders used in this corpus

| Placeholder | Meaning |
|-------------|---------|
| `sess_synthetic_pretooluse_bash` | session_id for a redacted PreToolUse Bash event |
| `tu_synthetic_<n>` | tool_use_id (sequential synthetic) |
| `/redacted/cwd` | cwd that the mapper must not echo as a label without the safe-cwd helper |
| `synthetic-repo` | placeholder repo name |
| `redacted-prompt-text` | UserPromptSubmit prompt placeholder |
| `synthetic-transcript-id` | transcript_path placeholder (not a real file path) |

## Coverage map

The mapper tests treat these files as a table; each row exercises one mapper
output assertion. New shapes go here, not inline string literals in the
test file, so changes stay reviewable.
