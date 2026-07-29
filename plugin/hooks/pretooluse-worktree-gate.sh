#!/usr/bin/env sh
# git-tools PreToolUse wrapper for worktree-gate: the first PreToolUse hook in
# hooks.json, ahead of forced-use -- a call this plugin's forced-use routing
# would otherwise redirect toward a git-tools subcommand still needs denying
# first if it targets a path outside a git worktree at all.
#
# worktree-gate is a plain hook binary (see worktree-gate/detect/hook.go): it
# reads the PreToolUse payload on stdin and, when it denies, writes the deny
# response to stdout -- this wrapper only resolves which binary to run and
# execs it unmodified, stdin and stdout untouched. Its own rollout staging
# (GIT_TOOLS_WORKTREE_GATE_ENFORCE) lives in the
# rollout package the binary links, not here.
#
# Resolution order: WORKTREE_GATE_BIN (bootstrap-worktree-gate.sh's
# provisioned, checksum-verified path), else `command -v worktree-gate`. Fail
# open (silent allow, no stdout) when neither resolves -- the same posture
# forced-use-hook.sh takes for an ungoverned or unavailable CLI.
set -eu

bin="${WORKTREE_GATE_BIN:-}"
if [ -z "${bin}" ] || [ ! -x "${bin}" ]; then
  bin="$(command -v worktree-gate 2>/dev/null || true)"
fi

if [ -z "${bin}" ]; then
  exit 0
fi

exec "${bin}"
