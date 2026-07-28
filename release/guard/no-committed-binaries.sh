#!/usr/bin/env bash
# SC-DISTRIBUTION guard: no built binary is committed to this tree. Every
# per-OS/arch artifact ships through release.yml (CD) instead, so a
# committed executable is always a defect here -- a CI runner cross-compiles
# every target for this repo, so no committed-binary path was ever needed.
#
# A candidate is a git-tracked file whose tracked mode is executable
# (100755) and whose leading bytes contain a NUL -- the same signal git's
# own diff machinery uses to call a blob "binary".
set -euo pipefail

root="${1:-.}"
violations=()

while IFS=$'\t' read -r meta path; do
  mode="${meta%% *}"
  [ "$mode" = "100755" ] || continue
  file="$root/$path"
  [ -f "$file" ] || continue
  if head -c 8000 "$file" | LC_ALL=C grep -qaP '\x00'; then
    violations+=("$path")
  fi
done < <(git -C "$root" ls-files -s)

if [ "${#violations[@]}" -gt 0 ]; then
  echo "no-committed-binaries: FAIL - committed binary file(s) found (SC-DISTRIBUTION forbids this; distribute via release.yml instead):" >&2
  printf '  - %s\n' "${violations[@]}" >&2
  exit 1
fi

echo "no-committed-binaries: OK - no committed binary found under $root"
