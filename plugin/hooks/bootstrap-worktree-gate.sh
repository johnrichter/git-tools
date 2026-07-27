#!/usr/bin/env sh
# git-tools SessionStart bootstrap: provisions the worktree-gate binary the
# same way bootstrap.sh provisions git-tools itself, through
# download-script.sh -- a second PF_CLI_NAME under the one pinned plugin
# version, since worktree-gate ships from this same repo and tag. Its
# provisioned path is exported under WORKTREE_GATE_BIN for
# pretooluse-worktree-gate.sh to pick up.
#
# Soft-fail posture matches bootstrap.sh: an unresolved binary here means
# pretooluse-worktree-gate.sh finds nothing at WORKTREE_GATE_BIN and falls
# back to `command -v worktree-gate`, and absent that too, fails open (the
# gate simply doesn't run rather than blocking every tool call).
set -eu

if [ -z "${CLAUDE_PLUGIN_ROOT:-}" ] || [ -z "${CLAUDE_PLUGIN_DATA:-}" ]; then
  echo "git-tools bootstrap-worktree-gate: CLAUDE_PLUGIN_ROOT/CLAUDE_PLUGIN_DATA not set -- skipping (not running under the plugin runtime?)" >&2
  exit 0
fi

export PF_CLI_NAME="worktree-gate"
export PF_BIN_ENV="WORKTREE_GATE_BIN"
export PF_PLUGIN_DATA="${CLAUDE_PLUGIN_DATA}"
export PF_VERSION_FILE="${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json"
export PF_RELEASE_BASE_URL="${GIT_TOOLS_RELEASE_BASE_URL:-https://github.com/johnrichter/git-tools/releases/download}"
export PF_ENV_FILE="${CLAUDE_ENV_FILE:-}"

exec "${CLAUDE_PLUGIN_ROOT}/hooks/download-script.sh"
