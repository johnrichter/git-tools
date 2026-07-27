#!/usr/bin/env bash
# SC-VERSIONING guard: a release tag's path prefix must equal this module's
# path from the repo root. git-tools' go.mod lives at the repo root itself
# (path "."), so unlike a monorepo module (tagged <path>/[v]X.Y.Z) there is
# no directory segment to require: the tag is a bare [v]X.Y.Z.
set -euo pipefail

if [ $# -ne 1 ]; then
  echo "usage: tag-prefix.sh <tag>" >&2
  exit 2
fi
tag="$1"

if [[ ! "$tag" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "tag-prefix: FAIL - '$tag' is not [v]X.Y.Z (SC-VERSIONING: tag prefix must equal the module's path from repo root, which is empty here)" >&2
  exit 1
fi

echo "tag-prefix: OK - '$tag' conforms to SC-VERSIONING"
