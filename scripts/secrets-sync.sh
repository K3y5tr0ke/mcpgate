#!/usr/bin/env bash
# secrets-sync.sh — drift reminder for gitignored local secret files.
#
# .env and configs/mcpgate.dev.yaml are NOT in git, so they must be kept in your
# password manager by hand. This is a checksum-based nag (no auto-sync):
#   check  — warn if a tracked secret file changed since the last `mark`
#   mark   — record the current checksums after you've updated your PM
#
# It never blocks (check always exits 0); it only reminds.
set -uo pipefail
cd "$(dirname "$0")/.."

FILES=(.env configs/mcpgate.dev.yaml)
BASELINE=".secrets-sync"

sha() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

case "${1:-check}" in
mark)
  : >"$BASELINE"
  for f in "${FILES[@]}"; do
    [ -f "$f" ] && printf '%s  %s\n' "$(sha "$f")" "$f" >>"$BASELINE"
  done
  echo "[secrets] baseline recorded — local secret files marked in-sync with your password manager."
  ;;
check)
  drift=0
  for f in "${FILES[@]}"; do
    [ -f "$f" ] || continue
    cur="$(sha "$f")"
    base="$(awk -v p="$f" '$2==p {print $1}' "$BASELINE" 2>/dev/null)"
    if [ -z "$base" ]; then
      echo "[secrets] ⚠ $f has no recorded baseline — back it up in your password manager, then: make secrets-synced"
      drift=1
    elif [ "$cur" != "$base" ]; then
      echo "[secrets] ⚠ $f changed since last sync — update your password manager, then: make secrets-synced"
      drift=1
    fi
  done
  [ "$drift" -eq 0 ] && echo "[secrets] ✓ local secret files match the last recorded baseline."
  exit 0 # reminder only — never blocks a push
  ;;
*)
  echo "usage: secrets-sync.sh [check|mark]"
  exit 2
  ;;
esac
