#!/usr/bin/env bash
# Fetch a complete GitHub stargazer snapshot without replacing a known-good
# output when the API fails, returns malformed data, or pagination is truncated.
set -euo pipefail

usage() {
  echo "usage: $0 --repo OWNER/REPO --output PATH" >&2
}

require_positive_integer() {
  local name="$1"
  local value="$2"
  if [[ ! "${value}" =~ ^[1-9][0-9]*$ ]]; then
    echo "${name} must be a positive integer, got '${value}'" >&2
    exit 2
  fi
}

repo=""
output=""
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --repo)
      repo="${2:-}"
      shift 2
      ;;
    --output)
      output="${2:-}"
      shift 2
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

if [[ -z "${repo}" || -z "${output}" ]]; then
  usage
  exit 2
fi

page_size="${STARGAZER_PAGE_SIZE:-100}"
max_pages="${STARGAZER_MAX_PAGES:-10}"
max_attempts="${STARGAZER_MAX_ATTEMPTS:-3}"
retry_seconds="${STARGAZER_RETRY_SECONDS:-2}"
require_positive_integer STARGAZER_PAGE_SIZE "${page_size}"
require_positive_integer STARGAZER_MAX_PAGES "${max_pages}"
require_positive_integer STARGAZER_MAX_ATTEMPTS "${max_attempts}"
if [[ ! "${retry_seconds}" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
  echo "STARGAZER_RETRY_SECONDS must be non-negative, got '${retry_seconds}'" >&2
  exit 2
fi

workdir="$(mktemp -d -t fetch-stargazers.XXXXXX)"
pending_output=""
cleanup() {
  rm -rf "${workdir}"
  if [[ -n "${pending_output}" ]]; then
    rm -f "${pending_output}"
  fi
}
trap cleanup EXIT
output_dir="$(dirname "${output}")"
output_name="$(basename "${output}")"
mkdir -p "${output_dir}"
pending_output="$(mktemp "${output_dir}/.${output_name}.XXXXXX")"

fetch_page() {
  local page="$1"
  local destination="$2"
  local attempt=1
  local response="${workdir}/response.json"
  local error_log="${workdir}/gh-error.log"
  local endpoint="repos/${repo}/stargazers?per_page=${page_size}&page=${page}"

  while [[ "${attempt}" -le "${max_attempts}" ]]; do
    : >"${response}"
    : >"${error_log}"
    # Plain listing only (no vnd.github.v3.star+json): this script only
    # ever diffs usernames, never starred_at, and the timestamped variant
    # gets rejected for the workflow's ephemeral GITHUB_TOKEN (403) even
    # though the same call succeeds with a user PAT.
    if gh api "${endpoint}" \
      >"${response}" 2>"${error_log}"; then
      if ! jq -e \
        'type == "array" and all(.[]; (.user.login? | type == "string" and length > 0))' \
        "${response}" >/dev/null 2>&1; then
        echo "invalid stargazer response for page ${page}: expected an array of user logins" >&2
        return 1
      fi
      mv "${response}" "${destination}"
      return 0
    fi

    echo "stargazer API request failed for page ${page} (attempt ${attempt}/${max_attempts})" >&2
    cat "${error_log}" >&2
    if [[ "${attempt}" -lt "${max_attempts}" ]] && [[ "${retry_seconds}" != "0" ]]; then
      sleep "${retry_seconds}"
    fi
    attempt=$((attempt + 1))
  done

  echo "failed to fetch stargazers page ${page} after ${max_attempts} attempts" >&2
  return 1
}

raw_usernames="${workdir}/usernames.txt"
: >"${raw_usernames}"
page=1
while [[ "${page}" -le "${max_pages}" ]]; do
  page_json="${workdir}/page-${page}.json"
  fetch_page "${page}" "${page_json}"
  count="$(jq -r 'length' "${page_json}")"
  if [[ "${count}" -eq 0 ]]; then
    break
  fi

  jq -r '.[].user.login' "${page_json}" >>"${raw_usernames}"
  if [[ "${count}" -lt "${page_size}" ]]; then
    break
  fi
  if [[ "${page}" -eq "${max_pages}" ]]; then
    echo "stargazer pagination reached ${max_pages} full pages; refusing a partial snapshot" >&2
    exit 1
  fi
  page=$((page + 1))
done

tr -d '\r' <"${raw_usernames}" | LC_ALL=C sort -u >"${pending_output}"
mv "${pending_output}" "${output}"
