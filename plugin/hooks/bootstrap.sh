#!/usr/bin/env sh
# git-tools SessionStart bootstrap: sets download-script.sh's PF_* env contract
# (see download-script.sh's own header) to the git-tools CLI's name, data dir
# and pinned version, then execs it unmodified. All provisioning logic lives
# in download-script.sh; this file only supplies git-tools' own values.
#
# PF_RELEASE_BASE_URL points at this repo's GitHub Releases download root
# (bare "vX.Y.Z" tags per SC-VERSIONING); download-script.sh resolves the
# rest of the URL against release.yml's actual archive + checksums.txt shape.
set -eu

if [ -z "${CLAUDE_PLUGIN_ROOT:-}" ] || [ -z "${CLAUDE_PLUGIN_DATA:-}" ]; then
  echo "git-tools bootstrap: CLAUDE_PLUGIN_ROOT/CLAUDE_PLUGIN_DATA not set -- skipping (not running under the plugin runtime?)" >&2
  exit 0
fi

export PF_CLI_NAME="git-tools"
export PF_PLUGIN_DATA="${CLAUDE_PLUGIN_DATA}"
export PF_VERSION_FILE="${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json"
export PF_RELEASE_BASE_URL="${GIT_TOOLS_RELEASE_BASE_URL:-https://github.com/johnrichter/git-tools/releases/download}"
export PF_ENV_FILE="${CLAUDE_ENV_FILE:-}"

exec "${CLAUDE_PLUGIN_ROOT}/hooks/download-script.sh"
