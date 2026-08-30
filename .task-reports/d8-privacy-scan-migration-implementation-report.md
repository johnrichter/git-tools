# D8 privacy-scan migration — implementation report (stage 4, shim)

## Scope of this report

D8 has four ordered steps. Steps 1-3 (differential corpus, Go migration into
`git-tools scan privacy`, and the corpus-wide proof) were implemented, reviewed, and
merged in a prior session, before this dispatch. This report covers the net-new work
done in this session: **step 4 only** — replace each of the seven `check_privacy.py`
copies with a thin shim over `git-tools scan privacy`, committed and pushed per repo.
Stages 2 (soak/reference-count measurement) and 3 (deletion + branch-protection admin
action) of the shim rollout are explicitly out of scope for this dispatch and are not
attempted here.

## Steps 1-3 — already done, verified still true

Confirmed via git-log archaeology that `chore/d8-privacy-scan-migration` is a
long-running integration branch, not fresh for this dispatch, and that the Go
migration landed already:

- Implementation: `89b4208`
- Quality review (verdict ACCEPT WITH FIXES): `b373c00`
- Merge to main: `d4a604c`

Re-checked this session, still true on `main`/this worktree:

- `go.mod` pins `github.com/johnrichter/claude-shared-tooling/go/githooks v0.6.0`.
- That pinned version does **not** fix divergence D-1 (see below) — confirmed by
  reading the module cache source directly
  (`privacy.go:109`'s internal-hostname regex still has no reserved-sentinel
  lookahead). This remains an open, already-filed dependency on a new
  `ai-shared-lib`/`go/githooks` tag + repin — a different repo, out of scope here,
  reported per instruction rather than silently skipped.
- Tier vocabulary cross-reference: `githooks.PrivacyTier`
  (`TierPublic`/`TierConfidential`/`TierPrivate`, `privacy.go:23-25`) is the one
  canonical Go-side source of the three-value posture list. `rust/frontmatter`'s
  `validate.rs` expresses a different vocabulary (tag-presence/shape, not this
  scan-posture enum) — no fourth copy of the three-value list was introduced by the
  shim's `TIER_MAP`; it maps Python's own `public`/`datadog`/`personal` names onto the
  one existing Go enum.

### Differential-corpus proof — citing the already-accepted result

The prior quality review (`.task-reports/d8-privacy-scan-migration-quality-review.md`)
already ran the corpus-wide proof honestly and it was accepted. This session attempted
a fresh re-run of the same committed harness
(`.task-reports/d8-differential-corpus-harness.py`) purely as an extra confirmation.
The re-run was aborted after 7 minutes (corpus-snapshot phase alone reached 5.3 GB on
disk, no comparison had completed) to avoid tying up shared disk/CPU for a
re-confirmation of a result already reviewed and accepted; the previously accepted
numbers below stand as this report's authoritative differential result.

| Repo | Tier (py -> go) | Files | Python findings | Go findings | Match |
|------|-----------------|-------|--------|----|-------|
| marketplace | public -> public | 552 | 0 | 1 | no |
| workspace | personal -> private | 400 | 0 | 0 | yes |
| knowledge-public-datadog | public -> public | 91 | 0 | 1 | no |
| knowledge-private-datadog | datadog -> confidential | 123 | 0 | 0 | yes |
| knowledge-private-personal | personal -> private | 30 | 0 | 0 | yes |
| marketplace-datadog | datadog -> confidential | 286 | 6 | 5 | no |
| ai-shared-lib-datadog | datadog -> confidential | 96 | 0 | 0 | yes |
| **Total** | | **1578** | **6** | **7** | **4 of 7** |

Every tracked file in all seven repos was covered (1578 files, not a sample). The 3
mismatches are the three already-filed, already-characterized divergences:

- **D-1** (real cutover blocker, both public-tier repos): `privacy.go:109`'s internal-
  hostname regex lacks Python's reserved-sentinel lookahead, so it false-positives on
  RFC 6761 sentinel hosts (`.test`/`.example`/`.localhost`/`.invalid`) that Python
  correctly excludes. Fix belongs in `ai-shared-lib`/`go/githooks`, requires a new tag
  and a repin — not fixable in this worktree.
- **D-2** (marketplace-datadog, justified): Go's `awsExampleAccessKeyIDs` allowlist
  exempts the canonical AWS-documentation example key
  (`AKIAIOSFODNN7EXAMPLE`); Python has no such allowlist. Go's behavior is better, not
  a bug.
- **D-3** (documented-intentional, zero real-world occurrences): Go's
  `hostTerminator` is positive/consuming where Python uses a negative lookahead,
  missing synthetic-only shapes like `http://192.168.1.5.attacker.io/x`.

None of the three divergences let real sensitive content through undetected; D-1 is
the one that matters for cutover because it produces a false FAIL on legitimately
clean content in the two public-tier repos.

## Step 4 — shim implementation (this session's net-new work)

### Design

Each of the seven `scripts/check_privacy.py` files is replaced body-for-body with a
93-line stdlib-only shim. The script's own CLI contract is preserved exactly, so every
existing caller (pre-commit hooks, CI) keeps working unmodified:

- `--tier {public,datadog,personal}` (required), `--root` (optional), `--strict`
  (flag).
- Exit 0 clean, exit 1 on FAIL — same as before.

The shim shells out to the provisioned `git-tools scan privacy` binary
(`/home/bits/.claude/plugins/data/governance-git-jr-claude-plugins/bin/git-tools`,
overridable via `GIT_TOOLS_BIN` for tests/harness use), maps the three tier names onto
git-tools' `public`/`confidential`/`private`, and parses the JSON envelope's
`data.privacy_violations_found`/`data.privacy_warnings_found` to re-derive the exact
same pass/fail decision `check_privacy.py` always made
(`violations or (strict and warnings)`).

This re-derivation is required, not optional: git-tools' own process exit code is
nonzero on *any* finding, including a bare warning with `--strict` unset — a stricter
convention than `check_privacy.py` ever had. Forwarding git-tools' raw exit code was
the first draft and was caught and rejected before rollout — it would have made every
repo with any non-strict internal-identifier warning start failing existing callers
that previously passed.

### Per-repo results

All seven landed as their own small commit on branch `chore/check-privacy-shim`,
message "Shim check_privacy.py over git-tools scan privacy", each branched off that
repo's own `main` (not the two other in-flight marketplace-datadog branches,
`chore/add-privacy-tier-config` / `chore/check-privacy-canonical-sync`, which were not
touched).

| Repo | Worktree | Commit | Pushed |
|------|----------|--------|--------|
| knowledge-private-personal | `.claude/worktrees/check-privacy-shim` | `8932b25` | yes — remote `chore/check-privacy-shim` = `8932b25` |
| workspace | `.claude/worktrees/check-privacy-shim` | `fd11fec` | yes — remote = `fd11fec` |
| marketplace | `.claude/worktrees/check-privacy-shim` | `f5a9f9f` | yes — remote = `f5a9f9f` |
| marketplace-datadog | `.claude/worktrees/check-privacy-shim` | `521e926` | **no** — see blocker below |
| knowledge-private-datadog | `.claude/worktrees/check-privacy-shim` | `f23cbd0` | yes — remote = `f23cbd0` |
| knowledge-public-datadog | `.claude/worktrees/check-privacy-shim` | `052e1b8` | yes — remote = `052e1b8` |
| ai-shared-lib-datadog | `.claude/worktrees/check-privacy-shim` | `c4d9216` | yes — remote = `c4d9216` |

Each worktree path is
`/home/bits/Development/workspaces/psa-platform/<repo>/.claude/worktrees/check-privacy-shim`.
Remote heads were re-verified this session via `git ls-remote origin
refs/heads/chore/check-privacy-shim` against each local commit — six of seven match
exactly; no PRs opened, no merges to any main, per instruction.

### marketplace-datadog push blocker (pre-existing, not a shim regression)

`git-tools push chore/check-privacy-shim` from that repo's worktree fails its own
pre-push secret-scan precondition (exit 30,
`precondition_unmet.secret_detected`, rule `slack_token`, path
`plugins/knowledge-agents/datadog-code-knowledge-agent/corpus/chunks.jsonl`, 30
findings). This is pre-existing real secret-shaped content in a large corpus fixture
(612 MB and 34 MB `chunks.jsonl` files), confirmed to fail identically under both the
old `check_privacy.py` and the new shim — not something the shim introduced or
regressed. `git-tools push` has no override flag for this by design. There is already
a separate, unmerged branch in that repo, `chore/secret-scan-exempt`, that appears
aimed at exactly this corpus-content problem; this task did not touch or rely on it.
The commit `521e926` exists locally on `chore/check-privacy-shim` in that repo's
worktree, ready to push once the corpus-content issue is resolved on its own branch.

### git-tools test suite (this worktree, re-run this session)

```
go build ./...        pass
go vet ./...           pass
go test ./... -count=1 pass (all packages ok, ~30s total)
```

No production code in git-tools changed this session (steps 1-3 were already merged);
this is a clean re-confirmation, not a new result.

## What's done / what's deferred

**Done:**
- Steps 1-3 (already merged, re-confirmed still true, D-1 dependency re-confirmed
  still open in the pinned `go/githooks v0.6.0`).
- Step 4, shim stage 1: all seven repos' `check_privacy.py` replaced with the shim,
  each committed on its own `chore/check-privacy-shim` branch off its own `main`; six
  of seven pushed and ready for review; marketplace-datadog committed but blocked from
  push by its own pre-existing, unrelated secret-scan gate finding.

**Explicitly deferred (per task instruction, not attempted):**
- Shim rollout stage 2 — soak period / reference-count measurement of the shim in
  production before deletion.
- Shim rollout stage 3 — deleting the seven full Python implementations and any
  branch-protection admin action.
- No PR opened or merge performed on any of the seven `chore/check-privacy-shim`
  branches.
- D-1 fix (new `ai-shared-lib`/`go/githooks` tag + repin) — different repo, filed as a
  pending dependency, not fixed here.
- marketplace-datadog's corpus secret-content issue — belongs to the separate,
  already-open `chore/secret-scan-exempt` branch in that repo.

## Files

- Report (this file), committed alongside this change in the git-tools worktree.
- Shim source, identical 93-line body across all seven repos:
  `<repo>/.claude/worktrees/check-privacy-shim/scripts/check_privacy.py`.
