# D6 quality review — `870d4fe`, `38a426a`

- **Reviewer:** quality-reviewer
- **Worktree:** `/home/bits/Development/workspaces/psa-platform/git-tools/.claude/worktrees/gate-classifier-bypass`
- **Branch:** `fix/worktree-gate-classifier-bypass`
- **Reviewed:** `870d4fe` (LED-023 / LED-153) and `38a426a` (classifyGit bare-flag)
- **Verdict:** **FIX-APPLIED** — one blocking fail-open regression in `870d4fe`, fixed in `47e614b`
- **Re-verification:** `go build ./...`, `go vet ./worktree-gate/...`, `gofmt -l worktree-gate/`, `go test ./worktree-gate/... -count=1` all clean on the committed tree; `detect` coverage 92.1%

## Evidence availability

Both prompt-cited evidence files are **absent**: `.task-reports/D6-report.md` and `.task-reports/D6-test-verification.md` do not exist in the worktree, in any commit reachable on this branch (`git log --all -- .task-reports` is empty), in the primary checkout, or in a stash. They were uncommitted and were destroyed with the interrupted attempt's working tree. `.task-reports/` carries no ignore rule, so nothing hid them.

Consequence: every claim attributed to the implementer's report and the test-engineer's PASS was re-derived from the tree rather than accepted. That is what surfaced the blocking finding below — it is not visible in either diff by reading, only by executing both revisions. Two claims remain unverifiable and are recorded as such:

- "the implementer tried the ledger's own literal proposed fix first and rejected it after it broke an existing pinned regression test" — unverifiable. Note that `870d4fe` **is** LED-023's literally-proposed fix, first clause; the rejected attempt was presumably the second clause ("exclude operands that cannot be a path — unbalanced quotes, pipes, a leading dot"). Not confirmable without the report.
- the test-engineer's specific counts (78 insertions / 0 deletions, 5 adversarial cases, LED-160 non-reproduction). The corpus diff being additive-only I did confirm directly.

## Blocking finding (fixed)

### B1 — `870d4fe` reopened a write into the primary checkout from a worktree cwd

`worktree-gate/detect/decide.go:567` (as committed at `870d4fe`) — replacing `namedPaths`' catch-all with the hand-curated `pathOperandCommands` map dropped operand judging for a class the catch-all was genuinely load-bearing for: the **package managers `verbs.json` already names as writers**. Their default write locus is the cwd, which the cwd leg judges — but every one of them takes a path-valued option that retargets that locus to somewhere the cwd leg never looks.

Measured by exporting both revisions (`git archive 870d4fe^ | tar -x -C /tmp/pre870`) and running identical inputs against each, cwd `= /repo/wt` (a legitimate worktree), target `/repo` (the primary checkout):

| Command | at `870d4fe^` | at `870d4fe` |
| --- | --- | --- |
| `npm install --prefix /repo` | DENY | **ALLOW** |
| `npm install --prefix /repo/sub` | DENY | **ALLOW** |
| `pip install -t /repo/site pkg` | DENY | **ALLOW** |
| `pip install --target /repo/site pkg` | DENY | **ALLOW** |
| `poetry install --directory /repo` | DENY | **ALLOW** |
| `gem install --install-dir /repo pkg` | DENY | **ALLOW** |
| `yarn add --cwd /repo pkg` | DENY | **ALLOW** |
| `go install /repo/tracked.md` | DENY | **ALLOW** |

This is exactly the case SC20 exists for — a write-class piece naming a path that resolves into a primary checkout, however spelled — and the deny was silently lost. It is invisible to the suite because no existing test and none of the 78 added corpus lines covers a package manager with a path-valued option, and invisible to a diff read because the curated list looks like a faithful extract of `write_prefixes` until you diff the two sets and notice `npm`/`yarn`/`pnpm`/`pip`/`pip3`/`poetry`/`go`/`cargo`/`bundle`/`gem` are all missing. The cwd-leg deny still fires from a *primary* cwd, which is why the shape reads as covered.

**Fix (`47e614b`):** `pathOperandCommand(v Verbs, cmd string)` now **derives** the set from `Verbs.WritePrefixes` — the command word of each entry via the existing `firstToken`, the same decomposition `containsGovernedWord` already uses — instead of restating it. `Verbs` is threaded into `namedPaths` and `namedPathDenial` (two internal call sites in `scanBash`; no test called either directly). `find` stays an explicit addition, documented as such, because its writing forms are `write_contains`-anchored (`-delete`, `-exec`) rather than `write_prefixes`-anchored. `cp`/`mv`/`ln` (`copyDestinations`) and `sed` (`sedInPlace`) are matched by earlier switch cases and never reach the derived one, so their more precise modeling is untouched.

Post-fix, both boundaries hold simultaneously (cwd `/repo/wt`): all eight rows above DENY again; `npm install`, `npm install lodash`, `pip install requests`, `go mod tidy` stay ALLOW; and the LED-023/153 allows (`jq`, `python3 -c`, `awk`, `"$DAT_TOOLS" render`, plain `sed`) stay ALLOW while `rm`, `sed -i`, `tee`, `mkdir`, `vim`, `find -delete` and a primary-landing redirect stay DENY.

## Review point 1 — is `pathOperandCommands` the right long-term design?

**No, and it needed fixing now, not disclosing as a residual.** The implementer's own report reportedly flagged the drift risk hypothetically ("*if* `verbs.json` later gains a new write-prefix command…"). The drift was not hypothetical — it was already present in the commit, against the `verbs.json` in the same tree, which is B1. A cross-check test alone would have been the wrong remedy: it would have gone red immediately and correctly, but the list, not the test, was the defect.

Deriving the set is strictly better than a synced list plus a drift test:

- classification and operand judging now change together by construction — a new `write_prefixes` entry cannot gain one without the other.
- it introduces no new trust surface: `verbs.json` already decides what is write-class at all, so deriving from it cannot widen anything a shrunken `write_prefixes` would not already widen at classification time.
- it is what LED-023's own proposed fix asked for in as many words: "only treat an operand as a write destination for commands **actually modeled as writers**". The curated map was a second, narrower model of the same thing — the ledger's wording pointed at the existing model.

The cross-check test is still added (`TestPathOperandCommand_CoversEveryWritePrefixVerb`), not as the mechanism but as a tripwire against a future return to a hand-maintained set; it also pins `find` and pins that `jq`/`python3`/`awk`/a variable-named tool are *not* path-operand commands.

## Review point 2 — `sedInPlace`'s exact boundary

`sedInPlace(args) == len(args) > 0 && strings.HasPrefix(args[0], "-i")`. Executed, not inferred:

| Spelling | `sedInPlace` | Classified write at all? | Verdict from worktree cwd, target in primary |
| --- | --- | --- | --- |
| `sed -i s/a/b/ /repo/f` | yes | yes (`write_prefixes` `"sed -i"`) | DENY |
| `sed -i.bak s/a/b/ /repo/f` | yes | yes | DENY |
| `sed -i '' s/a/b/ /repo/f` (BSD) | yes (`args[0]` is `-i`) | yes | DENY |
| `sed -e s/a/b/ -i /repo/f` | **no** | **no** — Uncertain | ALLOW |
| `sed --in-place s/a/b/ /repo/f` | **no** | **no** — Uncertain | ALLOW |
| `sed -ri s/a/b/ /repo/f` | **no** | **no** — Uncertain | ALLOW |

The three it does not handle are exactly the three the `"sed -i"` literal prefix anchor does not classify write in the first place, so `namedPaths` never sees them as writes — `sedInPlace` mirrors the anchor precisely and adds no gap of its own. Widening only `sedInPlace` would be inert (it would judge operands on a piece already Uncertain); the anchor is the thing to fix, which is FB9's, and the last three rows reproduce identically at `870d4fe^`, confirming pre-existing and out of scope.

**Correction to the review brief:** the brief states these spellings are "already covered elsewhere in this codebase's own test corpus". They are not. `-i.bak`, `-i ''`, `--in-place` and `-ri` appear nowhere in `worktree-gate/` (searched `.go`, `.json`, `verbs.json`, `fixtures.go`); the only `sed -i` coverage anywhere is the bare `sed -i s/a/b/` form, at `fixtures.go:84`, `decide-bash-corpus.json:12`, `:1002`, `decompose_test.go:205` and `decide_led023_led153_test.go:60`. The boundary was undocumented and unpinned. `47e614b` documents it in `sedInPlace`'s doc comment; pinning the FB9 half belongs to FB9.

## Review point 3 — `classifyGit`'s boundary against a stacked global flag

`gitSubcommand` (`decide.go:788`) handles the multi-flag case correctly, before and after `38a426a`, and the fix cannot reach it. Trace of `git -c foo=bar -c baz=qux status`: `i=0` `-c` matches the value-consuming case, `i++` skips `foo=bar`; loop `i++` → `i=2`; `-c` again, skips `baz=qux`; `i=4` `status` hits `default` and returns `("status", …)`. `sub != ""`, so the new guard is never entered and the ordinary read/write split governs. Executed: `git -c a=b -c c=d status` → ALLOW from both `/repo` and `/repo/wt`; `git -c a=b -c c=d commit -m x` → DENY from `/repo`, ALLOW from `/repo/wt`; identical at `870d4fe^`.

`gitSubcommand` returns `""` on exactly two shapes, both genuinely subcommand-free: every token consumed as a global option (including a trailing value-consuming option with no value, `git -c`), and a trailing bare `--`. No git invocation that performs a repo write can reach either — a write needs a verb word, which `default` would have returned. The guard is placed before `strings.ToLower(sub)`, so an unrecognized *real* subcommand is untouched, and the SDET table pins that directly (`frobnicate` → `ClassWrite`, `-C dir commit` → `ClassWrite`). The `rest` return value is unused on the new path, which is correct — there is nothing to split.

Residual, pre-existing, not fixed: `git help` and `git help worktree` still classify write (`help` is a real verb word this switch does not map) and so deny from a primary cwd, the same "denied for the wrong reason" shape `38a426a` fixed for `--help`. Identical at `870d4fe^`. Fixing it means adding read verbs to `gitReadSubcommands`, which is scope beyond this task's acceptance and fails closed, not open. Filed as plan feedback below.

## Review point 4 — LED-023's additional sighting shapes

`workspace/.dat/ledger.md:546–562` read in full. The occurrences paragraph cites, beyond the inline-program-string shape: a bare `--help` read, a semicolon-joined pair of read-only commands, a sanctioned `worktree add` denied by its own `--repo` flag, and "**this sweep session reproduced it directly**, repeatedly: a bare pipe, a `2>&1` compound, and a heredoc".

The implementer's claim that these are covered by the four **prior** commits, not by this task's two, **holds** — spot-checked by running each shape against both revisions from a worktree cwd, all ALLOW at both:

| Shape | `870d4fe^` | `38a426a` |
| --- | --- | --- |
| bare pipe: `git status \| head -5`, `ls -la \| grep tracked` | ALLOW | ALLOW |
| `2>&1` compound: `grep -rn foo . 2>&1 \| tail -5` | ALLOW | ALLOW |
| heredoc: `cat /repo/wt/tracked.md <<'EOF' … EOF` | ALLOW | ALLOW |
| ledger's literal example: `ls -d .dat 2>/dev/null \|\| echo none` | ALLOW | ALLOW |
| semicolon-joined reads: `pwd; ls` | ALLOW | ALLOW |
| `git --help`, `git --version` | ALLOW | ALLOW |

Worth recording precisely because the last row shows what `38a426a` does *not* do: `git --help` from a worktree cwd was already allowed before it, since the cwd is a worktree. `38a426a`'s value is confined to a **primary-checkout** cwd, which is exactly the topology its five added corpus cases use (`"cwd": "/repo"`). The commit's own message says this correctly ("denying it from a primary checkout"); the review brief's framing of it as fixing a general deny is looser than the commit's.

## Review point 5 — documentation and comment accuracy

- `namedPaths` doc comment, as committed at `870d4fe`: **inaccurate**, and its inaccuracy is B1's cover story. It called `pathOperandCommands` "the write_prefixes-anchored utilities" while the map omitted ten `write_prefixes` command words, and it explicitly classed "a package manager … a package name" as naming "no destination operand at all" — a statement that reads as a considered decision but is wrong for any package manager carrying `--prefix`/`-t`/`--target`/`--directory`/`--install-dir`/`--cwd`. Rewritten in `47e614b` to state the actual criterion (the write signal is the command word, not a shell-opened redirect).
- `pathOperandCommands` doc comment, as committed: **inaccurate** — "(see `write_prefixes`)" and "write_prefixes-anchored" describe a derivation the code did not perform. Its parenthetical about `find` was accurate and is preserved, now correctly labelled as the one non-`write_prefixes` addition.
- `sedInPlace` doc comment, as committed: accurate but incomplete — "mirroring write_prefixes' own `sed -i` anchor" was true and is the load-bearing fact, but it left the reader to assume which spellings that covers. Extended with the executed boundary from point 2 and FB9's ownership.
- `classifyGit` empty-subcommand comment (`decide.go:835–843`): **accurate as written**, no change needed. "gitSubcommand returns `""` only when it never found a subcommand token to skip options past" matches the implementation exactly, including both `""` return paths, and the distinction it draws from an unrecognized subcommand is the one the code makes and the SDET table pins.
- The `decide_sc23_gitignore_test.go:126` comment rewrite is accurate: `echo` is in `read_prefixes`, is not a `write_prefixes` command word, and reaches write class only via `>>`, so its operand `x` is correctly no longer a candidate destination and the denial is correctly the cwd leg's. Verdict preserved, mechanism assertion tightened from `via "/repo/x"` to `may modify`. Still true after `47e614b`.
- Comment density in both commits matches the surrounding file, which is heavily and deliberately commented (rule-ID cross-references, "do not narrow this back" notes). No dead references, no cross-language archaeology.

## Review point 6 — commit message and convention

Both `870d4fe` and `38a426a` match the branch's voice: sentence-case imperative subject on one line, blank line, wrapped explanatory body giving mechanism and rationale, ledger IDs inline, no bullet lists, no attribution trailer. `870d4fe`'s body correctly discloses the one test whose mechanism assertion changed, which is the disclosure a reviewer needs.

On the trailer: none of this branch's six commits carries one, and none of the last 20 commits on `main` does either (13 of the 40 before that do, all older). Current convention is no trailer, so `47e614b` follows it.

One accuracy note on `870d4fe`'s message, superseded by `47e614b` rather than left standing: "Verified against the full existing corpus and SDET suite with no verdict regressions" was true of the suite and false of the behavior. The suite had no case in the regressed class. Green-suite verification cannot support a no-regression claim about a rule whose whole job is to catch shapes the corpus does not enumerate; the claim needed differential execution against the parent revision, which is what found B1.

## Test-suite assessment

Adequate for what `38a426a` changed; **inadequate for `870d4fe`**, in one specific and instructive way.

- `38a426a`: good. The SDET table is genuinely adversarial — it pins both directions (`frobnicate` → write, `-C dir commit` → write) alongside the new read cases, so it would fail if the guard were misplaced after the `switch` or applied to an unrecognized verb. The five corpus cases use a primary cwd, the only topology where the fix is observable.
- `870d4fe`: the added tests are well-constructed for the bug as understood — the LED-023/153 tests correctly use a **worktree** cwd (a primary cwd would deny everything and hide a coverage loss, as the file's own comment says), and the negative test covers six real writes. The gap is that both the positive and negative sets were drawn from the commands the fix was *thinking about*. Nothing tested the class the fix silently dropped, so 92.1% coverage and a green suite were fully compatible with a reopened fail-open. The general lesson for the test-engineer: when a change **narrows** a conservative catch-all, the suite must enumerate what fell outside the new boundary and assert each case's verdict against the parent revision — coverage of the new branch is not coverage of the removed one.
- Gaps now closed by `47e614b`: `TestLED023_ModeledWriterOperandsStillJudged_PackageManagerRetarget` (eight restored denials plus four still-allowed non-retargeted counterparts, so it cannot pass by reverting to the old over-broad default) and `TestPathOperandCommand_CoversEveryWritePrefixVerb` (drift tripwire, guarded against vacuous pass on an empty `write_prefixes`).
- Gaps left open, for the test-engineer or a follow-up task: no corpus coverage of `sed`'s `-i.bak` / `-i ''` / `--in-place` / `-ri` spellings (FB9's scope); no coverage of `dd`'s `if=`/`of=` operand form (see residual R2).

## Residual risk

- **R1 — path-valued options are still unmodeled in general.** `47e614b` restores the *coarse* catch (every non-flag operand of a modeled writer is a candidate) rather than modeling which option carries a path. So `npm install --prefix /repo` denies because `/repo` is an operand, not because `--prefix` is understood; conversely `npm install $PKG` denies on the unexpandable operand, a false positive of the same coarseness that existed for all six prior commits and that no ledger entry has filed. Properly modeling per-tool path-valued options is a new task, not this one.
- **R2 — `dd if=… of=…` is not really judged.** `dd` is a modeled writer, but `operands()` returns `if=/x` and `of=/y` as single tokens, so the real destination is never resolved as a path: `dd if=/repo/wt/x of=/repo/tracked.md` from a worktree cwd is ALLOW, and from a primary cwd denies on the nonsense path `/repo/if=/repo/wt/x`. Verdicts happen to be defensible; the mechanism is not. Reproduces identically at `870d4fe^` — pre-existing, unchanged, out of scope. Same family as FB9. Worth filing.
- **R3 — `git help` classifies write** (point 3). Fails closed. Pre-existing.
- **R4 — `truncate -s 0 /repo/tracked.md` from a worktree cwd is ALLOW** (Uncertain class, allowed because the cwd is a worktree). Pre-existing and identical at `870d4fe^`; the general class of "unmodeled writer not in `verbs.json`" is `verbs.json`'s coverage problem, not this rule's.
- **R5 — the two evidence reports are unrecoverable.** Two of the implementer's and test-engineer's claims (listed above) could not be checked. Neither is load-bearing for this verdict, which rests on execution.

## Plan feedback

1. **A narrowing change to a fail-closed catch-all needs differential execution, not a green suite.** Fold into the task/review template: when a task's own description is "replace a conservative default with a narrower rule", the acceptance should require enumerating the set that falls outside the new boundary and asserting each member's verdict at both revisions. `870d4fe` passed an independent test-engineer PASS at 92.1% coverage with a reopened fail-open, because every added test was drawn from the same mental model as the fix.
2. **Prefer deriving a policy set from the existing model over restating it.** `pathOperandCommands` was a second model of `write_prefixes` and diverged on day one. Where a second list is genuinely unavoidable, a cross-check test is the minimum bar — but check first whether derivation is available, as it was here.
3. **`verbs.json`'s literal-prefix anchors are a systemic weak spot, not three separate bugs.** FB9 (`sed -i` flag ordering), R2 (`dd if=`/`of=`) and R4 (unmodeled writers like `truncate`) are one finding wearing three hats: a whitespace-sensitive prefix match over a command line is defeated by ordinary option reordering. Consider a task that matches on tokenized argv with an option-aware model instead of raw-string prefixes, and fold FB9 into it rather than fixing the `sed` case alone.
4. **`.task-reports/` should be committed as work proceeds, or written outside the worktree.** Losing both evidence artifacts to a discarded working tree cost this review its inputs. That the loss was recoverable-by-re-derivation is luck, not design — and here it was net positive only because re-derivation found B1.

## Files touched by this review

- `/home/bits/Development/workspaces/psa-platform/git-tools/.claude/worktrees/gate-classifier-bypass/worktree-gate/detect/decide.go`
- `/home/bits/Development/workspaces/psa-platform/git-tools/.claude/worktrees/gate-classifier-bypass/worktree-gate/detect/decide_led023_led153_test.go`
