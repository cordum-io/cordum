#!/usr/bin/env bash
# gen.sh — (re)generate the TEST-ONLY release keypair used by the binary
# integrity test harness. Writes ASCII-armored public + private halves to
# the current directory. NOT for production use; see README.md.
#
# Usage:
#   ./gen.sh                 # generate fresh keypair (random entropy)
#   ./gen.sh --deterministic # use a fixed Name/Email/Comment metadata so
#                            # re-runs differ only in entropy, not identity
set -euo pipefail

cd -- "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"

if ! command -v gpg >/dev/null 2>&1; then
  echo "gpg required (GnuPG 2.x)" >&2
  exit 2
fi

DETERMINISTIC=0
for arg in "$@"; do
  case "$arg" in
    --deterministic) DETERMINISTIC=1 ;;
    -h|--help)
      sed -n '1,/^set /p' "$0" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *) echo "unknown flag: $arg" >&2; exit 2 ;;
  esac
done

NAME_REAL="Cordum TEST-ONLY Release"
NAME_EMAIL="test-release@cordum.example"
NAME_COMMENT="TEST-ONLY synthetic key — NOT for production"

# Stable metadata when --deterministic; entropy still randomises the actual
# key material, which is fine for a committed test fixture.
if [ "$DETERMINISTIC" = "1" ]; then
  NAME_COMMENT="$NAME_COMMENT (deterministic-metadata seed v1)"
fi

WORK="$(mktemp -d -t cordum-test-keys-XXXXXX)"
trap 'rm -rf "$WORK"' EXIT
chmod 700 "$WORK"

cat >"$WORK/batch" <<EOF
%no-protection
Key-Type: RSA
Key-Length: 3072
Key-Usage: sign
Name-Real: $NAME_REAL
Name-Email: $NAME_EMAIL
Name-Comment: $NAME_COMMENT
Expire-Date: 0
%commit
EOF

gpg --homedir "$WORK" --batch --quiet --gen-key "$WORK/batch"

FPR=$(gpg --homedir "$WORK" --with-colons --list-keys "$NAME_EMAIL" \
  | awk -F: '$1=="fpr" {print $10; exit}')
if [ -z "$FPR" ]; then
  echo "gen.sh: failed to extract fingerprint" >&2
  exit 1
fi

gpg --homedir "$WORK" --batch --armor --export "$FPR" \
  >TEST-ONLY-release.pub.asc
gpg --homedir "$WORK" --batch --armor --export-secret-keys "$FPR" \
  >TEST-ONLY-release.priv.asc
chmod 644 TEST-ONLY-release.pub.asc TEST-ONLY-release.priv.asc

echo "TEST-ONLY-release fingerprint: $FPR"
echo "wrote TEST-ONLY-release.{pub,priv}.asc"
