# D8 quality review #2 — `check_privacy.py` shim over `git-tools scan privacy` (six repos)

## Verdict: FIX-APPLIED — 6 of 6 merged

Review #1 (`.task-reports/d8-check-privacy-shim-quality-review.md`) returned 0/6 on B1 (CI had no
git-tools binary), B2 (stale suites testing deleted code), M1, M2, M3, m1, m2. **All seven are
genuinely closed** — independently re-verified here, not taken from the fix report. The fix pass
introduced **one new defect** (a resolution tier that never fires for any real caller, plus an
inline comment asserting the opposite), which this review **fixed directly in all six repos**,
keeping the shim byte-identical, then re-verified and merged.

Reviewed at the fix-pass commits named in the dispatch, all six shim files byte-identical
(`md5 84a0102edef20ad9eef3dd4f3e93c727`) and all six test files byte-identical
(`md5 f17d2d567b47…`) before my edit.

## Re-verification of review #1's findings

### B1 — CI provisioning: CLOSED, with one prose/code discrepancy in the fix report

All five repos with `.github/workflows/ci.yml` carry an identical `Provision git-tools (pinned
v1.3.0, digest-verified)` step. Verified per repo, by reading the step and by parsing the YAML:

| Repo | Provision step | Scan step | Same job | Provision before scan | YAML |
|---|---|---|---|---|---|
| marketplace | `ci.yml:36` | `:54` | `guardrail` | yes | valid |
| workspace | `ci.yml:31` | `:50` | `guardrail` | yes | valid |
| knowledge-private-datadog | `ci.yml:25` | `:44` | `guardrail` | yes | valid |
| knowledge-public-datadog | `ci.yml:28` | `:47` | `guardrail` | yes | valid |
| ai-shared-lib-datadog | `ci.yml:34` | `:53` | `guardrail` | yes | valid |

- **Digest is the correct one.** Re-fetched both checksum files fresh from the live release. The
  release publishes **two** files, and the archive-vs-extracted-binary distinction that caused a
  real defect earlier in this session is handled **correctly** here:
  - `checksums.txt` (archives): `d721d992…` for `git-tools_1.3.0_linux_amd64.tar.gz`
  - `binary-checksums.txt` (extracted binaries): `1b48117682d9d15bdd3339898d90b54b35c86bac5b842a20cb9289c18a954dc6`
    for `git-tools_1.3.0_linux_amd64`

  The workflow pins `1b48117…` and compares it against `sha256sum` of the **extracted binary**.
  That is the `binary-checksums.txt` value, so pin and comparison target agree. Independently
  cross-confirmed on the arm64 pair (`3d15c724…` archive / `5b2a2b7a…` binary) by downloading and
  hashing both.
- **`GIT_TOOLS_BIN` is exported correctly and is visible to the later step.**
  `echo "GIT_TOOLS_BIN=$PWD/git-tools" >> "$GITHUB_ENV"` is the right mechanism, and provisioning
  is in the **same job** as the scan in all five (`$GITHUB_ENV` does not cross jobs).
- **No test step was deleted.** Review #1 recorded unittest steps in three of these workflows;
  they are intact as `python3 -m unittest tests.test_check_privacy -v`
  (workspace `:51`, knowledge-private-datadog `:45`, knowledge-public-datadog `:48`), inside the
  guardrail job after provisioning. `ai-shared-lib-datadog` still has no test step (pre-existing,
  unchanged). `marketplace` runs its suite in a separate `unit-tests` job with **no** provisioning
  — which is safe only because the rewritten suite is hermetic (confirmed below).

**Minor finding (not fixed, see rationale).** The step extracts **before** verifying:
`marketplace/.github/workflows/ci.yml:46-51` runs `tar xzf` and only then hashes the extracted
binary. Verify-**before-execute** holds (the digest gate precedes `chmod +x` and the
`GIT_TOOLS_BIN` export), which is the security-critical property, so this is hardening rather than
a live hole. The fix report's own step list claims it downloads `checksums.txt` and "verifies the
archive's sha256 before extracting anything" — **the workflow does neither**; it never fetches
`checksums.txt`. The code is safe; the report overstates it. Recommended tightening, for whoever
next touches these workflows: fetch `checksums.txt`, verify the archive, *then* extract, keeping
the existing extracted-binary check as a second gate.

### B2 — test-suite rewrite: CLOSED

Read in full in `marketplace` (public tier, suite runs in an unprovisioned separate job) and
`knowledge-private-personal` (private tier, no CI at all) — deliberately the two most different
CI shapes. All six files are in fact byte-identical (the fix report's claim that each was "adapted
to each repo's own style" is wrong, but identical is better here, and matches the identical shim).

- **Genuinely hermetic.** Every case patches `subprocess.run` and/or `resolve_git_tools`; no real
  binary is needed. Proven by running each suite with `GIT_TOOLS_BIN` and `CLAUDE_PLUGIN_DATA`
  unset and no `git-tools` on `$PATH` (`which git-tools` → not found): **6/6 repos pass.**
- **Coverage confirmed present:** tier mapping for all three names plus assertion that the *mapped*
  value reaches the command line; **all four** (violations × warnings × `--strict`) combinations —
  in fact all eight; unparseable stdout (non-JSON, empty, missing `data`, missing a count field);
  **both M1 failure modes** plus an exec-time `OSError`.
- **RFC 6761 sentinel case survives — but weakly, and it is the tracked-tree copy that does the
  real pinning.** `ReservedSentinelHostnameRegressionTests` is mock-only: it asserts that a
  git-tools verdict of 0/0 passes `--strict` and that 0/1 fails it. That tests the shim's
  arithmetic, not sentinel handling, and would not catch a git-tools regression. What *does* pin
  the fix is that the literal `foo.internal.test` survives in the file
  (`tests/test_check_privacy.py:210`, class docstring) and is therefore scanned as tracked content
  by each repo's own `--strict` guardrail — so a git-tools regression fails CI in
  `marketplace` and `knowledge-public-datadog`. The pin is real but incidental (a docstring
  mention); see Test-suite assessment.

### M1 — hardened resolution: CLOSED

`scripts/check_privacy.py:40-42` validates the override with `Path(...).is_file()` **and**
`os.access(..., os.X_OK)` before returning, and falls through when invalid.
`:82-86` wraps `subprocess.run` in `try/except OSError`. Reproduced all three modes live against
a real repo, each **exit 1, clean message, no traceback**:

| Mode | Output |
|---|---|
| `GIT_TOOLS_BIN=/nope/nonexistent` | `FAIL — git-tools binary not found (checked …)` |
| non-executable override (mode `0644`) | `FAIL — git-tools binary not found (checked …)` |
| executable but invalid exec format | `FAIL — could not run git-tools ([Errno 8] Exec format error: …)` |

### M2 — hardcoded operator path: CLOSED

The literal `/home/bits/.claude/plugins/data/governance-git-jr-claude-plugins/bin/git-tools` is
gone. Grepped `/home/bits`, `/Users/`, and `governance-git-jr-claude-plugins` across
`scripts/check_privacy.py`, `tests/test_check_privacy.py`, and `.github/workflows/ci.yml` in all
six: **0 matches.**

`CLAUDE_PLUGIN_DATA` is confirmed a real fleet convention, not a fix-report invention:
`marketplace/plugins/governance-git/hooks/bootstrap-worktree-gate.sh:21` names
`$CLAUDE_PLUGIN_DATA/bin/git-tools` literally, and `:268-277` treats it as the plugin runtime's own
signal. **But** `:268-269` is also the evidence for the new defect below — that hook warns
"CLAUDE_PLUGIN_DATA is not set -- skipping (not running under the plugin runtime?)", i.e. the
variable exists **only while the runtime is invoking a plugin hook**.

### M3, m1, m2 — CLOSED

- M3: stdout-format change documented, `scripts/check_privacy.py:13-15`.
- m1: description is now the repo-agnostic "Privacy guardrail (shim over git-tools scan privacy)"
  (`:64`), so byte-identity holds.
- m2: truncation-vs-count comment present (`:89-93`), correctly warning against deriving the
  verdict from `len(errors)`.

## New defect found and fixed

**N1 (major) — the `CLAUDE_PLUGIN_DATA` resolution tier never fires for any real caller, leaving
the shim unable to resolve git-tools locally at all; its inline comment asserted the opposite.**

Pre-edit `scripts/check_privacy.py:44-52` had exactly three tiers: `GIT_TOOLS_BIN` →
`$CLAUDE_PLUGIN_DATA/bin/git-tools` → `$PATH`. On this host, with no env overrides:

- `CLAUDE_PLUGIN_DATA` is **unset** in the shell, and `bootstrap-worktree-gate.sh:268` confirms it
  is exported only under the plugin runtime — not to shells and not to git hooks.
- `git-tools` is **not** on `$PATH` (`which git-tools` → not found).

Measured result, run in the marketplace worktree with ambient env:

```
FAIL — git-tools binary not found (checked GIT_TOOLS_BIN, CLAUDE_PLUGIN_DATA (unset), and $PATH)
EXIT=1
```

So the module's **own documented Usage block** (`:17-20`) failed on the fleet's own host, and every
local caller — all six `.githooks/pre-commit` invoke this script — would hard-fail. The comment at
`:46-48` claimed the tier "only resolves inside a Claude Code session"; it does not resolve there
either, as demonstrated. This is a regression: review #1 explicitly recorded that the local
pre-commit callers *were* fine pre-fix because the hardcoded path resolved. The fix pass
implemented half of M2's recommendation ("derive from `CLAUDE_PLUGIN_ROOT`/`$HOME`") and dropped
the `$HOME` half, producing a dead tier.

Severity is major rather than blocking because the actively-wired consumer — CI — sets
`GIT_TOOLS_BIN` explicitly and is unaffected, and because repo `.githooks` are currently
unreachable on this host anyway (`core.hooksPath` is redirected fleet-wide to
`/usr/local/lib/dd-git-hooks` in all six repos, whose `pre-commit` dispatcher additionally
references a `run-local-hooks` helper that is absent from that directory).

**Fix applied**, identically in all six repos. Kept `$CLAUDE_PLUGIN_DATA` as first preference and
added the `$HOME`-derived governance-git install path behind it, so real local callers resolve
while still committing no operator-specific absolute path. Also extended the override tier's
executability check to the provisioned tiers, so a present-but-non-executable install falls through
instead of being returned and then failing at exec:

```python
    plugin_data = os.environ.get("CLAUDE_PLUGIN_DATA")
    candidates = []
    if plugin_data:
        candidates.append(Path(plugin_data) / "bin" / "git-tools")
    candidates.append(
        Path.home() / ".claude" / "plugins" / "data"
        / "governance-git-jr-claude-plugins" / "bin" / "git-tools"
    )
    for candidate in candidates:
        if candidate.is_file() and os.access(candidate, os.X_OK):
            return str(candidate)
```

Tests extended by three cases (23 → 26): the `$HOME` tier resolving on its own, `CLAUDE_PLUGIN_DATA`
taking precedence over it, and a non-executable provisioned install being skipped. The negative
resolution cases previously relied on `patch.dict(os.environ, clear=True)` alone, which would have
**silently stopped testing the failure path** once a `$HOME` tier existed (they would have resolved
this host's real install and shelled out); they now go through a `_no_local_install()` helper that
stubs `$HOME` and `$PATH` together.

## Live RFC 6761 verification (done independently, not reused from the fix pass)

Downloaded a fresh `git-tools_1.3.0_linux_arm64` (native to this `aarch64` host) and verified it
myself at both layers before use:

- archive `3d15c724d3be6d9666df26964a15be8c82cd856e3f9e4ddceb70599cb612143e` — matches `checksums.txt`
- extracted binary `5b2a2b7a70cdc94f2d2379241cd8b5fd5dd6e4b3fcb6fc9b6bbde88e1dce19fa` — matches
  `binary-checksums.txt`

**Positive case.** `marketplace`, whose tracked tree contains `foo.internal.test`, scanned through
the shim at `--tier public --strict`: **0 violations, 0 warnings, exit 0.** The false positive
review #1 could not clear is gone.

**Negative control — the fix is narrow, not a blanket rule disable.** Staged a file containing a
genuine internal hostname (`prodbox01.internal.acmecorp.net`, no RFC 6761 reserved suffix) and
re-scanned: **1 warning, `rule: internal_identifier`**, exit 1 under `--strict`. So sentinel
hostnames are exempted while real internal hostnames still fire. Control file removed; worktree
left clean.

**Stale local plugin cache — unchanged, pre-existing, not a shim defect.** The install at
`/home/bits/.claude/plugins/data/governance-git-jr-claude-plugins/bin/git-tools` is still
`f34deca5b6070265e7f12112f27f9c2388416908d176c77bc7ff62ede230e57c` = **v0.10.0**, exactly as
review #1 recorded; v1.1.0/v1.2.0/v1.3.0 are all still missing. Consequence, now visible *because*
my fix restored local resolution: a bare local run in the two public-tier repos resolves that stale
binary and reports 1 sentinel warning (exit 1 under `--strict`). CI is unaffected — it provisions
and digest-verifies v1.3.0 itself. **The operator still needs to refresh the plugin install to
v1.3.0**; this is the same open item review #1 raised, not a new one.

## Re-verification after my fix

- **Byte-identity restored across all six**, both files: shim `md5 113449ebf48b…`,
  tests `md5 5d86a9044aca…` — verified pre-merge in the worktrees and post-merge on each `main`.
- **Hermetic suite: 26/26 pass in all six repos**, with `GIT_TOOLS_BIN` and `CLAUDE_PLUGIN_DATA`
  unset and nothing on `$PATH`.
- **End-to-end at each repo's real declared tier**, with the digest-verified v1.3.0 arm64 binary:

| Repo | `git-tools.yaml` `privacy_tier` | `--tier` arg | Suite | Real `--strict` run |
|---|---|---|---|---|
| marketplace | public | `public` | 26/26 | exit 0 |
| workspace | private | `personal` | 26/26 | exit 0 |
| knowledge-private-datadog | confidential | `datadog` | 26/26 | exit 0 |
| knowledge-public-datadog | public | `public` | 26/26 | exit 0 |
| ai-shared-lib-datadog | confidential | `datadog` | 26/26 | exit 0 |
| knowledge-private-personal | private | `personal` | 26/26 | exit 0 |

  Tier mapping confirmed correct against each repo's own declared tier
  (`public`→`public`, `confidential`→`datadog`, `private`→`personal`).
- **All five `ci.yml` still parse** after review (unmodified by me).

## Per-repo outcome

| Repo | Fix-pass commit | My fix commit | Merge → `main` | Pushed | Outcome |
|---|---|---|---|---|---|
| marketplace | `85e80fa` | `0c48156` | `b013b8b` (merge commit, 3 commits) | yes | **MERGED** |
| workspace | `5fb8909` | `fad04b9` | `fad04b9` (ff) | yes | **MERGED** |
| knowledge-private-datadog | `5869834` | `c129e31` | `c129e31` (ff) | yes | **MERGED** |
| knowledge-public-datadog | `0a093d3` | `8313615` | `8313615` (ff) | yes | **MERGED** |
| ai-shared-lib-datadog | `5a73b93` | `f4b3471` | `f4b3471` (ff) | yes | **MERGED** |
| knowledge-private-personal | `4e81931` | `5ce79ad` | `5ce79ad` (ff) | yes | **MERGED** |

Merged: **6 / 6**. Fixed: **6** (N1, shared shim + suite, byte-identical). Blocked: **0**.

Every merge and push went through the provisioned `git-tools merge` / `git-tools push main` from
that repo's own primary checkout, each repo independently; every signing gate reported
`already_signed`. All six working trees left clean.

`marketplace-datadog` was not touched, per scope.

## Test-suite assessment

Adequate to accept. Hermetic, fast, and now covering every resolution tier and its precedence.
Two gaps the test-engineer should close, neither blocking:

1. **The sentinel regression test is mock-only** (`tests/test_check_privacy.py:208-228`). It cannot
   fail if git-tools regresses on RFC 6761; the actual pin is the incidental `foo.internal.test`
   string in a docstring at `:210`. Make the pin deliberate — either an explicitly-named fixture
   constant that a comment marks as load-bearing tracked content, or an opt-in integration test
   that runs the real binary when `GIT_TOOLS_BIN` is set and skips otherwise.
2. **No test asserts the stdout echo**, which M3 documents as an intentional contract change. One
   case pinning that git-tools' JSON reaches stdout unmodified would stop a future "let's print a
   friendly summary" edit from silently breaking log consumers.

## Residual risk

- **Local plugin install is still v0.10.0.** Until the operator refreshes it, bare local
  `--strict` runs in the two public-tier repos report the sentinel warning and exit 1. CI is
  unaffected. Pre-existing; carried over from review #1.
- **CI provisioning is syntax- and digest-verified only.** GitHub Actions cannot be executed
  locally, so the first real `main` push is the first true exercise of the provisioning step. The
  pinned amd64 digest was verified against the live release, and the arm64 equivalent was verified
  by full download-extract-hash-run, which exercises the same code path.
- **Archive is extracted before it is verified** (minor finding above). Verify-before-execute holds.
- **`marketplace-datadog` still carries the old hardcoded operator path** in
  `.claude/worktrees/check-privacy-shim/scripts/check_privacy.py`. Out of scope for both reviews,
  but it means M2's disclosure fix is not yet fleet-wide — worth a follow-up task.
- **Three repos lack a `__pycache__` gitignore entry** (knowledge-public-datadog,
  knowledge-private-datadog, knowledge-private-personal): running the suite locally dirties the
  tree with untracked bytecode. Pre-existing and unrelated to this change; not fixed here.

## Plan feedback

- **Review #1's "enumerate every execution environment of every caller" lesson was applied to CI
  and then missed for the local callers.** The fix pass verified the CI environment thoroughly and
  assumed the plugin-runtime env var was present in the others. The generalisable rule is stronger
  than "enumerate environments": **for each resolution tier, prove it actually fires in at least
  one real environment.** A tier that never fires is dead code that reads as a working fallback —
  the same unit-green-but-not-wired-in class as review #1's B2, and unit tests stayed green through
  it because they mocked the resolver.
- **A fix report's prose must be verified against its own diff.** Two claims here did not match the
  code: the archive-checksum verification step (does not exist) and per-repo test-suite adaptation
  (files are byte-identical). Neither caused a defect, but both would have been accepted as
  evidence had they not been re-checked — and the archive-vs-binary checksum distinction is exactly
  where this session already produced one real defect.
- **Byte-identical files across six repos need a guard, not a convention.** Identity was preserved
  here only because each pass remembered to propagate. A cheap CI check — hash
  `scripts/check_privacy.py` and compare against a committed expected digest — would make drift
  fail loudly instead of silently.
