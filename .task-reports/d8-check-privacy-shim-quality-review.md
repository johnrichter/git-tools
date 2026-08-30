# D8 quality review — `check_privacy.py` shim over `git-tools scan privacy` (six repos)

## Verdict: RETURN — 0 of 6 merged

The shim's own translation logic is sound, but the migration is **incomplete**: it replaced
`scripts/check_privacy.py` without updating the two things that consume it. Five of the six
repositories' CI privacy-guardrail jobs fail on the first push to `main`, and all six ship a
`tests/test_check_privacy.py` that fails against the new file. Nothing was merged. No worktree
was edited — the fix requires a design decision (how CI obtains the binary) that belongs to the
implementer and plan owner, not to a reviewer's in-place patch.

Reviewed at these commits, all six shim files **byte-identical** (`md5 023c8e2beddd21bcd2c1c90cfe78103c`):

| Repo | Branch commit | Real tier (`git-tools.yaml`) | Shim CLI tier |
|---|---|---|---|
| knowledge-private-personal | `8932b25` | `private` | `personal` |
| workspace | `fd11fec` | `private` | `personal` |
| marketplace | `f5a9f9f` | `public` | `public` |
| knowledge-private-datadog | `f23cbd0` | `confidential` | `datadog` |
| knowledge-public-datadog | `052e1b8` | `public` | `public` |
| ai-shared-lib-datadog | `c4d9216` | `confidential` | `datadog` |

## Preconditions confirmed

- **git-tools v1.3.0 repin is real and on `marketplace` main.** `main` = `be653cb` ("Quality review:
  governance-git repin v1.3.0 -- FIX-APPLIED"), parent `61ae664` performs the repin.
  `plugins/governance-git/.claude-plugin/plugin.json` is at `0.17.0`;
  `plugins/governance-git/data/binary-digests.json` carries `"tag": "v1.3.0"`.
- **The RFC 6761 fix is real.** `git-tools` main = `b375b8d` ("Consume go/githooks v0.6.1: RFC 6761
  sentinel hostname false positive"); `go.mod:11` pins `go/githooks v0.6.1`; tag `v1.3.0` exists.
- **Per-repo diff scope is clean.** `git diff --stat main...HEAD` in every one of the six shows
  exactly one changed file, `scripts/check_privacy.py`, and nothing else. (A two-dot
  `git diff main` in `marketplace` misleadingly shows 18 files — that branch was cut before the
  v1.3.0 repin landed on `main`, so two-dot diff renders `main`'s newer commits as reversed
  removals. Use the three-dot form.)

## Findings

### Blocking

**B1 — CI cannot resolve the `git-tools` binary; the privacy-guardrail job fails on every push.**
`scripts/check_privacy.py:39-50` resolves the binary from exactly three sources: `GIT_TOOLS_BIN`,
the hardcoded host path `/home/bits/.claude/plugins/data/governance-git-jr-claude-plugins/bin/git-tools`,
and `$PATH`. On a GitHub-hosted `ubuntu-latest` runner none of the three resolves: the workflows
do `actions/checkout` + `actions/setup-python` and then invoke the script directly, with **no step
that installs, downloads, or provisions `git-tools`** (verified by reading each `ci.yml`). The shim
then takes `check_privacy.py:64-66` and exits 1:

```
FAIL — git-tools binary not found (checked GIT_TOOLS_BIN, <path>, and $PATH)
```

Reproduced directly: a copy of the shim with the host path repointed at a nonexistent location,
run with `GIT_TOOLS_BIN` unset and `PATH=/usr/bin:/bin`, exits 1 with that message on a clean tree.

Affected call sites:

| Repo | CI invocation | Effect |
|---|---|---|
| marketplace | `.github/workflows/ci.yml:37` | `guardrail` job fails; **every** downstream job is gated on it via `needs:` |
| knowledge-public-datadog | `.github/workflows/ci.yml:29` | guardrail job fails |
| workspace | `.github/workflows/ci.yml:32` | guardrail job fails |
| knowledge-private-datadog | `.github/workflows/ci.yml:26` | guardrail job fails |
| ai-shared-lib-datadog | `.github/workflows/ci.yml:35` | guardrail job fails |
| knowledge-private-personal | no `.github/workflows` at all | not affected (local `.githooks/pre-commit` only) |

This directly contradicts the task's stated acceptance — "preserving the exact same CLI contract
… so every existing caller (pre-commit hooks, CI) keeps working unmodified". The CLI *contract*
is preserved; the CLI's *dependency* is not, and CI is where that gap lands. The local
`.githooks/pre-commit` callers are fine, because the host path resolves on this machine.

**B2 — every repo's existing `tests/test_check_privacy.py` tests internals the shim deleted, and
five of six CI configs run it.** Those suites load the module via
`importlib.util.spec_from_file_location` and exercise the native checker's constants, regexes, and
scan functions, none of which survive in a 93-line subprocess wrapper. Measured, per repo:

| Repo | Result | CI runs it? |
|---|---|---|
| marketplace | 50 tests: 13 failures, 33 errors | yes — `ci.yml:69` (`unittest discover -s tests`) |
| knowledge-public-datadog | 50 tests: 13 failures, 33 errors | yes — `ci.yml:31` |
| knowledge-private-datadog | 50 tests: 13 failures, 33 errors | yes — `ci.yml:28` |
| knowledge-private-personal | 50 tests: 13 failures, 33 errors | no CI (latent) |
| workspace | 4 tests: 2 failures | yes — `ci.yml:34` |
| ai-shared-lib-datadog | 15 tests: 15 errors | no test step in `ci.yml` (latent) |

Two distinct failure classes, both real: (a) `errors` — attribute/import failures for removed
module internals; (b) `failures` — behavioural assertions that still make sense but no longer
hold, notably the suites' assertions that stdout contains `WARN` / `FAIL`, which the shim replaces
with git-tools' raw JSON envelope. The suites must be rewritten to the shim's actual surface
(subprocess invocation, tier mapping, exit-code derivation, binary-resolution fallbacks) — not
deleted, and not weakened.

### Major

**M1 — `GIT_TOOLS_BIN` is returned unvalidated, so a bad override crashes with a traceback.**
`check_privacy.py:40-42` returns `os.environ["GIT_TOOLS_BIN"]` with no `is_file`/executable check,
unlike the host-path branch at `:43-44`. `subprocess.run` at `:72` then raises an uncaught
`FileNotFoundError` (or `PermissionError` for a non-executable path), printing a Python stack trace
instead of the documented clean `FAIL — …` / exit 1. Reproduced: `GIT_TOOLS_BIN=/nope/git-tools`
yields a traceback. The task's own step-1 criterion ("… → clear error") holds only for the
nothing-found-anywhere path. Fix: validate the override the same way, and wrap `:72` in
`try/except OSError` returning the `FAIL — …` / 1 contract.

**M2 — a machine-specific absolute path is committed into six repositories, two of them public.**
`check_privacy.py:36` hardcodes `/home/bits/.claude/plugins/data/governance-git-jr-claude-plugins/bin/git-tools`.
Two problems. Portability: this single non-`$PATH` fallback is the *only* reason the shim works
anywhere, and its absence is the whole of B1. Disclosure: it publishes an operator's home-directory
name and plugin-install layout in `marketplace` and `knowledge-public-datadog`, both public-tier.
Prefer deriving the plugin data directory from `CLAUDE_PLUGIN_ROOT`/`$HOME` rather than pinning one
operator's absolute path, and let `$PATH` plus `GIT_TOOLS_BIN` carry the rest.

**M3 — stdout format changed, and that is not covered by "same CLI contract".** The shim echoes
git-tools' JSON envelope verbatim (`check_privacy.py:73-76`) before parsing it. Flags and exit codes
are preserved; human-readable output is not. This is the proximate cause of the `failures` half of
B2 and will surprise anyone reading guardrail logs. Either render a short human summary from the
parsed counts, or state the stdout change explicitly in the module docstring and in the migration
notes.

### Minor

**m1 — wrong repo name in `--help` for five of six repos.** `check_privacy.py:54` sets the argparse
description to "Marketplace privacy guardrail (shim over git-tools scan privacy)". Byte-identical
distribution means `workspace`, both `knowledge-private-*`, `knowledge-public-datadog`, and
`ai-shared-lib-datadog` all announce themselves as the marketplace guardrail.

**m2 — worth a comment: the count fields read are the un-truncated ones.** `check_privacy.py:79-81`
derives the verdict from `data.privacy_violations_found` / `data.privacy_warnings_found`. That is
correct and deliberate — the adjacent `errors[]` array that the shim *prints* is capped at 50 and
discloses `caveats.githooks.findings_truncated`. Since the printed array and the counted fields
differ in truncation behaviour, one line of comment would stop a future reader from "simplifying"
the check into `len(errors)`.

## Environment limitation (not a shim defect)

**The locally provisioned binary is `git-tools v0.10.0`, not the pinned v1.3.0.** The file at
`/home/bits/.claude/plugins/data/governance-git-jr-claude-plugins/bin/git-tools` has
`sha256 f34deca5b6070265e7f12112f27f9c2388416908d176c77bc7ff62ede230e57c`. That value matches the
`git-tools_0.10.0_linux_arm64` row (this host is `Linux aarch64`), retired from
`binary-digests.json` by `marketplace` commit `8ec29bb`; the pinned v1.3.0 row is
`1b48117682d9d15bdd3339898d90b54b35c86bac5b842a20cb9289c18a954dc6`. The plugin cache is three
releases stale (v1.1.0, v1.2.0, v1.3.0 all missing).

Consequence for the two public-tier repos, i.e. the check this review was asked to make: the RFC 6761
reserved-sentinel false positive **is still live locally**, because the binary predates the fix.
Both `marketplace` and `knowledge-public-datadog` produce, at their real tier:

- `--tier public` (no `--strict`): **exit 0**, 0 violations, 3 warnings.
- `--tier public --strict` (how CI and `.githooks/pre-commit` actually invoke it): **exit 1**,
  0 violations, 3 warnings — all three `rule: internal_identifier`, all three in
  `tests/test_check_privacy.py`, which is precisely that suite's own reserved-sentinel fixture.

This is the expected stale-plugin-cache limitation the operator refreshes separately, **not** a
defect in the shim: the shim's code is version-agnostic, and the sentinel behaviour is entirely
the binary's. It does mean the public-tier no-false-fail criterion **could not be positively
verified on this host** — it will need re-running once v1.3.0 is provisioned. Note also that if
B2's rewrite drops the sentinel fixture from `tests/test_check_privacy.py`, the false positive
disappears for the wrong reason; keep an equivalent sentinel fixture so the fix stays pinned.

The other four repos are clean at their real tier under the stale binary — 0 violations, 0 warnings,
exit 0 both with and without `--strict`. No run crashed and none hung.

## Per-repo outcome

| Repo | Shim run at real tier | One-file diff | Outcome |
|---|---|---|---|
| knowledge-private-personal | `--tier personal` → 0/0, exit 0 (strict and plain) | yes | **BLOCKED** — B2 (no CI, latent) |
| workspace | `--tier personal` → 0/0, exit 0 (strict and plain) | yes | **BLOCKED** — B1 + B2 |
| marketplace | `--tier public` → 0 viol / 3 warn; exit 0 plain, 1 strict (stale binary) | yes | **BLOCKED** — B1 + B2 |
| knowledge-private-datadog | `--tier datadog` → 0/0, exit 0 (strict and plain) | yes | **BLOCKED** — B1 + B2 |
| knowledge-public-datadog | `--tier public` → 0 viol / 3 warn; exit 0 plain, 1 strict (stale binary) | yes | **BLOCKED** — B1 + B2 |
| ai-shared-lib-datadog | `--tier datadog` → 0/0, exit 0 (strict and plain) | yes | **BLOCKED** — B1 + B2 |

Merged: **0 / 6**. Fixed: **0** (no shim edit made — see verdict). Blocked: **6**.

`marketplace-datadog` was not touched, per scope. No other branch in any repo was touched.

## Fix task for the implementer

Apply to all six worktrees, keeping the six files byte-identical:

1. **Give CI the binary (B1).** Add a provisioning step to each of the five `ci.yml` guardrail jobs
   that downloads the pinned `git-tools` release, verifies it against the `binary-digests.json` row
   for the runner's `(os, arch)`, and exports `GIT_TOOLS_BIN` — the same digest-verify-before-use
   discipline the plugin provisioner already uses. Do not settle for an unverified download.
2. **Rewrite the six `tests/test_check_privacy.py` suites against the shim's real surface (B2)** —
   tier mapping for all three names, exit-code derivation for the four
   (violations × warnings × `--strict`) combinations, unparseable-stdout handling, and each
   binary-resolution branch including both failure modes from M1. Keep a reserved-sentinel fixture.
   Do not delete or weaken coverage to go green.
3. **Harden binary resolution (M1)** — validate `GIT_TOOLS_BIN`, and catch `OSError` around the
   `subprocess.run` call so every failure path returns the `FAIL — …` / exit 1 contract.
4. **Stop hardcoding one operator's home path (M2)**; derive the plugin data directory instead.
5. **Fix the argparse description per repo (m1)** and add M3's stdout note plus m2's one-line comment.

Then re-run, per repo, at that repo's real tier with `--strict`, plus the rewritten suite, and
re-check the two public-tier repos **after** v1.3.0 is actually provisioned.

## Plan feedback

- The plan treated "preserve the CLI contract" as sufficient for caller compatibility. It is not:
  a shim swaps a **pure-stdlib** dependency footprint for an **external-binary** one, and every
  execution environment that runs the caller has to be re-checked for that binary. The GitHub
  runners were not. Any future in-binary migration of a guardrail script should carry an explicit
  "enumerate every execution environment of every caller" step.
- The plan did not account for the six pre-existing `tests/test_check_privacy.py` suites at all,
  even though five CI configs run them. Replacing a module while leaving its unit tests in place is
  the unit-green-but-not-wired-in failure class: the shim's own behaviour was fine in isolation,
  and the breakage sat entirely in the consumers.
- Landing this batch is gated on the operator refreshing the local plugin install to v1.3.0.
  Until then the public-tier no-false-fail criterion cannot be positively demonstrated on this
  host, so the two public repos should merge last, after a re-run.
