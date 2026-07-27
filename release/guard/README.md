# release/guard

The SC-VERSIONING and SC-DISTRIBUTION guards `ci.yml`'s `guardrail` job runs on every push,
pull request and tag push.

- `tag-prefix.sh <tag>` -- SC-VERSIONING: a release tag's path prefix must equal this
  module's path from the repo root. git-tools carries exactly one Go module, at the
  repo root itself, so that path is empty and the required tag is a bare `[v]X.Y.Z` --
  unlike a monorepo module, which tags `<path>/[v]X.Y.Z`.
- `no-committed-binaries.sh [root]` -- SC-DISTRIBUTION: no built binary may be committed to
  this tree; every per-OS/arch artifact (git-tools and worktree-gate alike) ships through
  `release.yml` (CD) instead.

Both scripts are stdlib-shell, no install: run directly from a checkout.

```sh
bash release/guard/tag-prefix.sh v1.2.3
bash release/guard/no-committed-binaries.sh .
```

Exit codes: 0 conforms; 1 a violation was found; 2 usage error.
