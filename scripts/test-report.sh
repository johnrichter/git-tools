#!/usr/bin/env bash
# Runs `go test` for one package under a wall-clock timeout and, on a
# forced failure/timeout, reports exactly what a caller needs to resume:
# tests completed, tests remaining, elapsed time, an ETA for the rest, and
# a ready-to-run resume command with a suggested -timeout already filled
# in. On a clean pass it just reports the pass -- the report only earns
# its keep when something didn't finish.
#
# Usage: scripts/test-report.sh <package> <timeout> [extra go test args...]
#   package   e.g. ./internal/cli/...
#   timeout   a `timeout(1)`-shaped duration, e.g. 30m, 90s
#
# Runs the package's whole test census (no -run in [extra go test args...]
# -- this wrapper computes -run itself, for the resume command).
#
# Requires jq (already a repo dependency, see surface-hygiene.sh).
set -euo pipefail

if [ "$#" -lt 2 ]; then
  echo "usage: $0 <package> <timeout> [extra go test args...]" >&2
  exit 1
fi

package=$1
budget=$2
shift 2
extra_args=("$@")

if ! command -v jq >/dev/null 2>&1; then
  echo "test-report: FAIL - jq is required" >&2
  exit 1
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
events="$work/events.jsonl"
names="$work/names.txt"

# The full set of top-level tests this run is responsible for, known up
# front so "remaining" never requires the caller to guess or recount.
go test -list '.*' "$package" | grep -v '^ok' > "$names" || true
total=$(wc -l < "$names" | tr -d ' ')

start=$(date +%s)
set +e
timeout --signal=KILL "$budget" go test -json "$package" "${extra_args[@]}" > "$events"
run_status=$?
set -e
elapsed=$(( $(date +%s) - start ))

# Top-level (non-subtest) results only: a subtest's Test field contains a
# "/", and only top-level names appear in the -list census above.
completed_names=$(jq -r 'select(.Action=="pass" or .Action=="fail" or .Action=="skip") | select(.Test != null and (.Test | contains("/") | not)) | .Test' "$events" | sort -u)
completed=$(printf '%s\n' "$completed_names" | grep -c . || true)
failed=$(jq -r 'select(.Action=="fail") | select(.Test != null and (.Test | contains("/") | not)) | .Test' "$events" | sort -u | grep -c . || true)

# A clean exit from `go test` itself (not from the timeout(1) wrapper)
# means the whole census ran and passed; report that plainly rather than
# building the detailed remaining-work report below.
if [ "$run_status" -eq 0 ]; then
  printf 'ok   - %s: %d tests passed in %ds\n' "$package" "$completed" "$elapsed"
  exit 0
fi

remaining_names=$(comm -23 <(sort -u "$names") <(printf '%s\n' "$completed_names"))
remaining=$(printf '%s\n' "$remaining_names" | grep -c . || true)

# Rough per-test average from what actually ran; used only to size the
# ETA and the suggested resume timeout, never presented as exact.
if [ "$completed" -gt 0 ]; then
  avg=$(( elapsed / completed ))
else
  avg=$elapsed
fi
[ "$avg" -lt 1 ] && avg=1
eta=$(( avg * remaining ))
# 50% margin over the estimate, floored at 60s, so the suggested value is
# one a caller can paste and expect to succeed rather than recompute.
suggested_timeout=$(( eta + eta / 2 ))
[ "$suggested_timeout" -lt 60 ] && suggested_timeout=60
suggested_timeout_str="${suggested_timeout}s"

resume_run=$(printf '^(%s)$' "$(printf '%s' "$remaining_names" | paste -sd '|' -)")

{
  printf 'FAIL - %s did not finish within %s (exit %d)\n' "$package" "$budget" "$run_status"
  printf '  tests completed:  %d/%d\n' "$completed" "$total"
  printf '  tests remaining:  %d\n' "$remaining"
  printf '  tests failed:     %d\n' "$failed"
  printf '  elapsed:          %ds\n' "$elapsed"
  printf '  ETA to finish remaining: ~%ds\n' "$eta"
  printf '  resume command:\n'
  printf "    go test -run '%s' -timeout %s %s %s\n" "$resume_run" "$suggested_timeout_str" "$package" "${extra_args[*]}"
} >&2

exit 1
