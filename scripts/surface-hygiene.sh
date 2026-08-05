#!/usr/bin/env bash
# SC10/SC11 surface-hygiene guard, git-tools half. SC11 sweeps this whole
# repo for any banned name; SC10 sweeps the same name list again, narrowed
# to worktree-gate/ (the gate), asserting the gate itself advertises no
# bypass. One contract artifact --
# worktree-gate/detect/contracts/banned-names.json -- is the source for
# both sweeps: no name is hardcoded here, and its digest is pinned by
# worktree-gate/detect/contract_test.go, so this script and that test can
# never see divergent lists.
#
# Self-contained: this repo only, no sibling checkout, no environment
# variable to gate on. Prints every hit as path:line and exits non-zero on
# any; zero hits exits 0.
set -euo pipefail

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$HERE/.." && pwd)
CONTRACT="$REPO_ROOT/worktree-gate/detect/contracts/banned-names.json"

if ! command -v jq >/dev/null 2>&1; then
  echo "surface-hygiene: FAIL - jq is required to read $CONTRACT" >&2
  exit 1
fi
if [ ! -f "$CONTRACT" ]; then
  echo "surface-hygiene: FAIL - banned-name contract artifact missing: $CONTRACT" >&2
  exit 1
fi

mapfile -t banned_names < <(jq -r '.banned_names[]' "$CONTRACT")
if [ "${#banned_names[@]}" -eq 0 ]; then
  echo "surface-hygiene: FAIL - parsed zero banned names from $CONTRACT" >&2
  exit 1
fi

joined=$(printf '%s|' "${banned_names[@]}")
pattern="\\<(${joined%|})\\>"

violations=$(mktemp)
trap 'rm -f "$violations"' EXIT

# --- SC11: the banned-name set, zero hits anywhere in the repo -------------
find "$REPO_ROOT" \
  \( -name '.git' -o -path '*/.claude/worktrees' \) -prune -o \
  -type f \( -name '*.sh' -o -name '*.go' -o -name '*.md' \) \
  ! -name '*_test.go' ! -name '*_test.sh' \
  -print0 |
xargs -0 -r grep -HnE "$pattern" 2>/dev/null |
sed 's/^/SC11 banned name: /' >> "$violations" || true

# --- SC10: the same set, narrowed to worktree-gate/ (the gate itself) ------
gate="$REPO_ROOT/worktree-gate"
if [ -d "$gate" ]; then
  find "$gate" \
    \( -name '.git' -o -path '*/.claude/worktrees' \) -prune -o \
    -type f \( -name '*.go' -o -name '*.md' \) \
    ! -name '*_test.go' \
    -print0 |
  xargs -0 -r grep -HnE "$pattern" 2>/dev/null |
  sed 's/^/SC10 bypass name (gate): /' >> "$violations" || true
fi

count=$(wc -l < "$violations" | tr -d ' ')

if [ "$count" -gt 0 ]; then
  sort -u "$violations" | while IFS= read -r line; do
    printf 'FAIL - %s\n' "$line"
  done
else
  printf 'ok   - SC10/SC11 surface hygiene: zero hits across %s\n' "$REPO_ROOT"
fi

printf '\n%d violation(s)\n' "$count"
[ "$count" -eq 0 ]
