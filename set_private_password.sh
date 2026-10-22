#!/usr/bin/env bash
# set_private_password.sh — set the login for the /private data room on
# the deployed server.
#
#   ./set_private_password.sh                 # random 16-char password, printed
#   ./set_private_password.sh 'my-password'   # this password
#   ./set_private_password.sh 'my-password' alice   # for user "alice" (default: breedos)
#   ./set_private_password.sh 'my-password' mynotes notes   # areas: data (tables, /private), notes (/mynotes); default data
#
# The server stores only sha256(user:password) in <engine>/private/auth; the
# password itself is sent nowhere — the hash is computed here and written
# over ssh. Other users in the file are kept. The target comes from
# BREEDOS_DEPLOY_TARGET (env or .env next to this script), the same as
# deploy_breedos.sh. The binary re-reads the file on every request, so no
# restart is needed.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ -z "${BREEDOS_DEPLOY_TARGET:-}" ] && [ -f "$here/.env" ]; then
  # shellcheck disable=SC1091
  . "$here/.env"
fi
target="${BREEDOS_DEPLOY_TARGET:-}"
if [ -z "$target" ]; then
  echo "set BREEDOS_DEPLOY_TARGET=user@host:/absolute/path/to/engine/ (or put it in .env)" >&2
  exit 2
fi
host="${target%%:*}"
engine="${target#*:}"

password="${1:-}"
user="${2:-breedos}"
areas="${3:-data}"
case "$areas" in *:*|*' '*) echo "areas: comma-separated, e.g. data,notes" >&2; exit 2;; esac
case "$user" in *:*|*' '*|'') echo "user must be one word without ':'" >&2; exit 2;; esac
if [ -z "$password" ]; then
  password="$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 16)"
  generated=1
else
  generated=0
fi

hash="$(printf '%s:%s' "$user" "$password" | sha256sum | cut -d' ' -f1)"
line="$user:$hash:$areas"

# Replace this user's line, keep the others, create the file if missing.
ssh -o BatchMode=yes "$host" "set -e; d='$engine/private'; mkdir -p \"\$d/data\"; chmod 700 \"\$d\"; f=\"\$d/auth\"; touch \"\$f\"; chmod 600 \"\$f\"; { grep -v -E '^$user:' \"\$f\" || true; echo '$line'; } > \"\$f.new\"; mv \"\$f.new\" \"\$f\"; chmod 600 \"\$f\"; echo \"users now: \$(grep -c -v -E '^#|^\$' \"\$f\")\""

echo "user:     $user  (areas: $areas)"
if [ "$generated" = 1 ]; then
  echo "password: $password   (generated — keep it somewhere outside git)"
else
  echo "password: set as given"
fi
echo "login at: https://www.breedos.org/private"
