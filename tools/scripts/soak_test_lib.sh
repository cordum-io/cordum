#!/usr/bin/env bash

# Count responses that violate their declared expectation. Lines without an
# expectation retain the soak test's default contract: every 4xx/5xx response
# is an error. Malformed curl status output is always unexpected.
count_unexpected_http_responses() {
  local http_log="$1"
  awk '
    {
      status = $3
      expected = ""
      if ($4 ~ /^expected=[0-9][0-9][0-9]$/) {
        expected = substr($4, 10)
      }
      if (status !~ /^[0-9][0-9][0-9]$/) {
        unexpected++
      } else if (expected != "") {
        if (status != expected) unexpected++
      } else if (status >= 400) {
        unexpected++
      }
    }
    END { print unexpected + 0 }
  ' "${http_log}"
}

# Emit endpoints whose unexpected 4xx responses should participate in retry-
# storm detection. Expected negative probes are deliberately excluded.
unexpected_client_error_endpoints() {
  local http_log="$1"
  awk '
    {
      status = $3
      expected = ""
      if ($4 ~ /^expected=[0-9][0-9][0-9]$/) {
        expected = substr($4, 10)
      }
      if (status ~ /^[0-9][0-9][0-9]$/ && status >= 400 && status < 500 &&
          !(expected != "" && status == expected)) {
        print $2
      }
    }
  ' "${http_log}"
}

# Rank repeated Compose log messages without an early-closing `head` stage.
# Under `set -o pipefail`, head caused upstream sort to receive SIGPIPE and
# abort the entire soak analysis before it could write its JSON result.
top_repeated_log_lines() {
  sed -E 's/^[^[:space:]]+[[:space:]]+\|[[:space:]]*//' |
    LC_ALL=C sort |
    uniq -c |
    sort -rn |
    sed -n '1,5p'
}
