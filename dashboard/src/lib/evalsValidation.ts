/**
 * Dataset name regex mirrors the server-side contract at
 * `core/model/eval_dataset.go` (EvalDataset.Name). Any change here must be
 * made in lockstep with the backend so the client catches invalid names
 * before the 400 round-trip.
 */
export const EVAL_DATASET_NAME_REGEX = /^[a-z0-9][a-z0-9_-]{2,63}$/;

export const EVAL_DATASET_NAME_HINT =
  "3–64 chars, lowercase letters/digits/_/-, starting with a letter or digit.";

export const MAX_EVAL_EXTRACT_ENTRIES = 10_000;
export const DEFAULT_EVAL_EXTRACT_ENTRIES = 1_000;

export const DEFAULT_EVAL_EXTRACT_VERDICTS = ["deny", "require_approval"] as const;
