---
name: git-tools — Gate Classification Layer + Relative-Path Resolution
description: "git-tools fix project, one repo, two milestones. Milestone A: five worktree-gate classification defects -- FB2/FB4/FB5, a merge-base write-prefix collision, and three read prefixes that admit genuine writes. Milestone B: a cwd-vs-repo relative-path bug (FB8) in worktree add. FB1, B2, and the governance-git re-pin are hand-offs."
id: project:git-tools-gate-classification-and-path-resolution:design
tags:
  - type:project
  - topic:tooling
  - topic:git-governance
  - team:psa
  - status:complete
  - privacy:public
  - owner:public
links:
  - project:git-governance-emergency-followup:design
  - project:git-governance-emergency-followup:feedback-review-record
  - project:dat-single-workspace-plans-and-programs:design
updated: 2026-08-27T18:09:54Z
---

# git-tools — Gate Classification Layer + Relative-Path Resolution

## Context & problem

The `worktree-gate` binary screens every Bash call in a governed repo. The gate denies a call that it classifies as repo-modifying outside a git worktree.

The classification layer is wrong in both directions today. Four defects over-block read-only commands. A fifth under-blocks: three read prefixes admit their own destructive subcommands, so `git remote add`, `git branch -D`, and `git tag -d` pass the gate in a primary checkout. All five live in one layer and share two predicates.

A second, unrelated defect lives in the `git-tools` CLI. `worktree add` creates a worktree correctly. Then the command fails its own post-condition check and returns exit 90.

The `magistrate` agent produced the first version of this brief during the post-build review of `git-governance-emergency-followup`. The `design-architect` agent then verified every claim against source. This planning session reproduced two of the defects live, without trying to:

- `find <dir> -name '*.jsonl' 2>/dev/null` — denied, naming `*.jsonl` as a write target. The same command without the redirect — allowed. This is A1.
- `git -C <dir> remote -v` — denied, though the command only reads. This is A3.

Verification status of every claim in this document: **proven against source**. The `design-architect` critique confirmed each defect, corrected three citations, and found three blocking gaps. This version applies those corrections. Line references drift, so re-verify before editing code.

## Why now

Three reasons make this the right time.

1. **A4 blocks other work.** `dat-single-workspace-plans-and-programs` §6 item 1 needs `git merge-base <integration> <branch>` from a primary checkout. The gate denies that command today. That proposal cannot proceed until A4 lands.
2. **The friction is constant.** A1 hit six times in one earlier session, and twice more in this planning session. Every hit costs a retry and a workaround.
3. **One release covers all of it.** FB9 showed that splitting a `git-tools` release into several passes creates its own coordination hazard. The consumer re-pin must move in lockstep. Fewer releases means less coupling risk.

## Goals & non-goals

- **Goals:** Remove the four over-block defects in the gate classification layer. Close the three under-blocks found on the same surface. Remove the cwd-vs-repo path defect in `worktree add`. Add a regression guard for each fix, in both directions. Ship one `git-tools` release.
- **Non-goals:** FB1, the model-roster rank gap — different repo, different surface, see §0. B2, the same path bug in the external git library — different repo, see §2. The `governance-git` consumer re-pin — different repo, see §3. Any change to the gate's fail-closed default for an unmodeled command. **Any read verb added without a recorded denial or a cited consumer requirement** — `git-tools` codifies a small set of sanctioned flows and does not mirror git's command surface. Any new verb in the `git-tools` CLI itself.

## Success criteria

Each criterion is observable and testable.

1. `git status 2>/dev/null` from a primary checkout is allowed. Today it is denied.
2. `find <path> -name '<glob>' 2>/dev/null` from a primary checkout is allowed. Today it is denied.
3. `echo hi > file.txt` remains denied outside a worktree. `TestClassifyBash_WritesAlwaysTrip` stays green.
4. `tool run >1` classifies as a write. Today it classifies as an fd-duplication and raises no write signal.
5. `git -C <dir> status` is allowed. Today it is denied.
6. `git merge-base <a> <b>` is allowed. Today it is denied.
7. `git remote add`, `git branch -D`, and `git tag -d` are denied outside a worktree. Today all three are allowed.
8. These read forms stay allowed, all of which the gate allows today: bare `git remote`, `git remote -v`, `git remote show origin`, bare `git branch`, `git branch -a`, `git branch -r`, `git branch -v`, `git branch --list`, `git branch --show-current`, `git branch --contains <commit>`, bare `git tag`, `git tag -l '<pattern>'`, and `git tag -n`. The split table carries the full enumeration.
9. Every git verb classifies the same way with a leading global option (`-C`, `-c`, `--git-dir`, `--work-tree`, `--namespace`) and without one.
10. The migration changes no classification other than the five defects. `git worktree list`, `git config --get`, `git config --list`, and `git reflog show` stay allowed. Bare `git worktree add`, `git worktree remove`, `git worktree prune`, `git config --set`, `git config --unset`, `git config --add`, and every `git stash` form stay denied outside a worktree.
11. No read verb is added without a recorded denial or a cited consumer requirement. `git merge-base` is the only addition this project makes.
12. `git-tools worktree add <relative-path> <ref> --repo <dir>` from any process cwd returns exit 0. Today it returns exit 90 when the cwd differs from the repo working tree.
13. `decide-bash-corpus.json` carries, per fix, one `want_deny:false` case for the removed over-block and one `want_deny:true` case proving no genuine write became an allow.
14. The full `worktree-gate` and `git-tools` test suites pass.

## Scope

**One repo: `git-tools`.** File surface:

| Surface | Milestone | Change |
| --- | --- | --- |
| `worktree-gate/detect/bash.go` | A | `classifyPiece` discard-target handling, `isFdDupTarget`, and the new in-code git read and write subcommand sets |
| `worktree-gate/detect/decide.go` | A | `namedPaths` operand scope, reuse of `gitSubcommand` |
| `worktree-gate/detect/verbs.json` | A | Remove every `git …` entry. Git moves to code. The file keeps only non-git prefixes. |
| `worktree-gate/detect/verbs.go` | A | Adjust the loaded-artifact validation if a class empties after the git entries leave |
| `worktree-gate/detect/bash_test.go` | A | Flip the A21 pin, keep the genuine-write anchors |
| `worktree-gate/detect/adversarial_test.go` | A | Extend `TestDefaultVerbs_CriticalVerbsPresent` |
| `worktree-gate/detect/testdata/decide-bash-corpus.json` | A | Flip one pinned case, add both-direction cases |
| `internal/cli/worktree.go` | B | Path base, doc comment, triage text |

The `contract-digests` set — `cwd-corpus.json`, `trackingdocs.json`, `connectors.json`, `banned-names.json` — is **not** touched. This project changes no contract artifact.

Landing the release re-pins a **separate** repo, the `governance-git` plugin. That re-pin is a declared cross-repo hand-off, not part of this repo's `file_surface`. §3 records the mechanics. B2 is handled the same way.

## 0 — Excluded: FB1, the model-roster rank gap

FB1 is the `plan-with-team` Phase 0 roster hard-stop. It is **not** part of this project. It lives in the `ai-shared-lib` checkout (GitHub repository name: `claude-shared-tooling`) and in the marketplace plugin. It shares no code with anything here.

The gap is real and verified: `go/roster/model-roster.json` gives `models["claude-opus-5"].cross_family_rank = null`, while `opus-4-8` holds rank 5 and `fable-5` holds rank 7. Slot 6 is vacant.

Fix it directly, outside project machinery, because it is small and cross-repo. The block is trivially worked around — plan from any ranked Opus, which is item 5's own remedy. Do not call it an unbreakable bootstrap loop.

1. `ai-shared-lib/go/roster/model-roster.json` — set `models["claude-opus-5"].cross_family_rank = 6`. **Operator ratification required.** `cross_family_rank` is an operator ruling, not a vendor fact. Rank 6 is a high-confidence inference from the existing sequence and the recorded "Opus above Sonnet" ruling. Also extend `_cross_family_order.note`, which names `opus-4-6/4-7/4-8` above Sonnet but omits `opus-5`. Bump `_cross_family_order.as_of` and `_as_of`.
2. Re-point the two tests that enshrine the gap onto a still-unranked example — `claude-opus-4-5` and `claude-sonnet-4-5`, the deliberate allowlist-parity carries whose vendor-fact fields are null by design. The tests are `roster/roster_test.go::TestCompareCrossFamilyMissingRankErrors` and `roster/adversarial_test.go::TestCompareOneSideDeclaredOneUndeclaredErrors`. `TestCompareUnknownIDPropagatesStaleNotDefaultOrdering` uses `opus-5` only as the known side and needs no change.
3. Regenerate `ai-shared-lib/go/anthropic-specifications.json` from the updated roster.
4. **Add the recurrence guard.** Refuse a model row that carries sourced vendor facts but no rank. The checkable invariant is `knowledge_cutoff != null ⇒ cross_family_rank != null`. That invariant separates `opus-5`, the bug, from `opus-4-5` and `sonnet-4-5`, the intentional null-rank carries whose vendor-fact fields are also null. Without this guard, the next model added reproduces FB1 exactly.
5. The Phase 0 exit-3 message must name the one-step operator remedy: switch the session model to an Opus tier and re-run. Roster regeneration is a maintainer action and must not be the only remedy offered. Do **not** add a hardcoded fallback. The skill forbids it, and `Compare`'s refuse-unless-both-declared invariant is correct.

Item 1 unblocks the front door. Items 4 and 5 stop the recurrence and stop the message reading as "the skill is broken."

## 1 — Milestone A: the gate classification layer

Five defects, one layer, one corpus. Fix them as two coherent changes. A1 and A2 refine the same redirect predicate in opposite directions. A3, A4, and A5 are one git-classification change.

### A1 — FB2, a redirect flips a read to write-class (criticality 16)

`classifyPiece` (`bash.go:468-471`) returns `ClassWrite` on `p.writesFile` before it consults `ReadPrefixes`. `decompose` sets `writesFile` at `bash.go:235-240`. A `2>/dev/null` on a modeled read command therefore makes the whole piece write-class. The `default` branch of `namedPaths` (`decide.go:328-334`) then returns every non-flag operand as a candidate write destination. A search pattern, a glob, or a read path is judged a write target.

**Do not reorder the checks.** Consulting `ReadPrefixes` before `writesFile` looks like the fix and is wrong. `echo` is a read prefix (`verbs.json:33`), and `bash_test.go:42` pins `echo hi > file.txt` as `ClassWrite`. A reorder would let a genuine write through.

The fix has two loci:

1. **`bash.go`** — treat `/dev/null` as a discard target that can never be a repo write. Keep `writesFile → ClassWrite` intact for every real target. This one change removes most of the observed friction.
2. **`decide.go`** — restrict operand-as-destination to commands actually modeled as writers. Judge an unmodeled command by its redirect targets alone.

The `/dev/null` change is small, safe, and independently valuable. The operand-scope change is the deeper fix and needs corpus work to land safely.

**A1 must flip a pinned fixture.** `decide-bash-corpus.json:400-404` asserts `git status 2>/dev/null` at `/repo` → `want_deny:true`. That case pins the bug. Flip it to `false`.

### A2 — FB4, a bare-digit redirect target reads as an fd-duplication (criticality 4)

`isFdDupTarget` (`bash.go:351-365`) strips a leading `&`, then treats an all-digits token as an fd-duplication. A redirect to a file literally named `2` therefore raises no write signal.

**This is a deliberate deferral, not an oversight.** `bash_test.go:97-99` pins `{"tool run >1", false, false}` under the comment "A21 (deferred): a bare-digit target reads as a duplication, not the file bash would create. Pinned as-is — neither widened nor narrowed." The corpus `description` repeats the deferral.

**Decision: un-defer and fix.** The reversal costs one predicate change and closes the both-directions story that A1 opens. A1 is a false positive, a discard read as a write. A2 is a false negative, a real write read as a discard. Both live in the same predicate. Reviewing them together is the only way to keep the predicate coherent.

The plan must edit the A21 pins, not leave them red:

- Flip `bash_test.go:97-99` from `{"tool run >1", false, false}` to `{"tool run >1", true, true}`.
- Rewrite the A21 comment in `bash_test.go` and the A21 paragraph in the corpus `description`. Record that the deferral was reversed, and why.

### A3 — FB5, a leading git global option defeats every prefix (criticality 4)

`read_prefixes` and `write_prefixes` anchor on a bare `git <subcommand>` (`verbs.json:2-105`). Reads match at a word boundary through `hasCommandPrefix` (`bash.go:587-593`). `git -C <dir> status` therefore matches no read prefix and no write prefix. The command classifies `ClassUncertain` and the gate denies it (`decide.go:191-197`), though the command only reads.

### A4 — a write prefix swallows a longer read verb

`WritePrefixes` match through unanchored `strings.HasPrefix` (`bash.go:480-484`). `ReadPrefixes` require a word boundary. The doc comment at `bash.go:29-31` records the asymmetry. `"git merge"` is a write prefix (`verbs.json:44`), so `git merge-base` matches it and classifies as a write. `"git stash"` (`verbs.json:53`) swallows `git stash list` and `git stash show` the same way. `merge-base` is the only one of the three this project unblocks. The two `stash` read forms stay denied, for the reason recorded under Key decisions and tradeoffs.

### A5 — three read prefixes admit genuine writes (found during this planning session)

`git remote`, `git branch`, and `git tag` sit in `read_prefixes`. `hasCommandPrefix` matches at a word boundary, so every subcommand of the three classifies as a read. Their destructive forms therefore pass the gate in a primary checkout today.

Measured directly by calling `classifyPiece` against the shipped `verbs.json` in this worktree. `0` is `ClassUncertain`, `1` is `ClassRead`, `2` is `ClassWrite` (`bash.go:13-19`):

| Command | Class today | Correct class |
| --- | --- | --- |
| `git remote add origin git@x:y.git` | 1 read | write |
| `git branch -D feature` | 1 read | write |
| `git tag -d v1` | 1 read | write |
| `git remote -v` | 1 read | read |
| `git -C /d status` | 0 uncertain | read |
| `git merge-base a b` | 2 write | read |
| `git stash list` | 2 write | read |
| `git config --get-regexp x` | 0 uncertain | read, but out of scope — see below |

A1 through A4 are over-blocks. A5 is an under-block, and an under-block on a safety control is the failure that matters. A5 lands with A3 and A4 because the refactor rewrites these exact three entries. Carrying them across unchanged would knowingly preserve the bypass.

`git config --get-regexp` appears in the table for completeness only. It is denied today and stays denied. `hasCommandPrefix` requires the prefix to end at a space, so `--get-regexp` never matches the `git config --get` entry. No recorded operation needs the command, so the evidence-only rule keeps it out, the same way it keeps out `git stash list`. `git config --get-all` is denied today for the same reason and stays denied. The table records the correct class so a future denial has a documented answer waiting.

### A3, A4, and A5 — one fix

Classify `git` in code, by resolved subcommand. Reuse `gitSubcommand` (`decide.go:434`), which already skips git's global options in their split, glued, and `=`-joined forms, and returns the subcommand token. Match that token against explicit read and write sets. Keep `verbs.json` for every non-git command.

Duplicating git's command surface as flat prefix strings is what produced A3 and A4. Reusing the existing helper removes the duplication rather than adding a second copy of it.

**Where the sets live.** The git read and write subcommand sets move into code, next to `gitSubcommand`. `verbs.json` keeps only non-git prefixes, and every `git …` entry leaves it. A flat prefix list cannot express "this subcommand, under any global option", which is exactly what A3 requires, and the `Verbs` struct (`bash.go:22-38`) is a flat `[]string` today. Changing that schema would be a larger change than moving twenty entries into code.

**The migration rule: preserve today's classification for every git subcommand, except where a listed defect requires a change.** Twenty git read prefixes and twenty-six git write prefixes exist today. Several of them are multi-token, and a subcommand token alone cannot carry them. Each one below must survive the migration as an explicit split. None of these is a new verb. Getting any of them wrong in the read direction recreates A5's bypass on a different command.

| Entry today | Read forms | Write forms |
| --- | --- | --- |
| `git worktree list` (read), `git worktree add`/`remove`/`prune` (write) | `list` | `add`, `remove`, `prune`, `move`, `repair`, `lock`, `unlock` |
| `git config --get`/`--list` (read), `--set`/`--unset`/`--add` (write) | `--get`, `--list` | `--set`, `--unset`, `--add`, `--replace-all`, and a bare `<name> <value>` |
| `git reflog show` (read) | `show` | `expire`, `delete`, `drop` |
| `git stash` (write) | none | every form, including `list` and `show` |

`git stash list` and `git stash show` stay denied. A4 names the collision correctly — `git stash` swallows both through unanchored matching — but no recorded operation needs either command. Under a subcommand scheme `stash` classifies as write, which reaches the same denial by a correct route. Adding a read form without evidence is exactly the sprawl this project refuses.

**The three defect splits.** A5 requires these, and only these, to change behavior:

Enumerate both directions for each. Today all three classify as read in every form, so an incomplete read list turns a working command into a denial. That is the friction this project exists to remove, so the read side must be enumerated fully, not sketched.

- `git remote` — **read:** bare, `-v`, `--verbose`, `show`, `get-url`. **Write:** `add`, `remove`, `rm`, `rename`, `set-url`, `set-head`, `set-branches`, `prune`, `update`.
- `git branch` — **read:** bare, `-a`, `--all`, `-r`, `--remotes`, `-v`, `-vv`, `--verbose`, `-l`, `--list`, `--show-current`, `--contains`, `--no-contains`, `--merged`, `--no-merged`, `--points-at`, `--format`, `--sort`, `--column`, `--color`. **Write:** `-d`, `-D`, `--delete`, `-m`, `-M`, `--move`, `-c`, `-C`, `--copy`, `-f`, `--force`, `--set-upstream-to`, `--unset-upstream`, `--edit-description`, or a positional operand with no listing flag present.
- `git tag` — **read:** bare, `-l`, `--list`, `-n`, `-v`, `--verify`, `--contains`, `--no-contains`, `--points-at`, `--merged`, `--no-merged`, `--sort`, `--format`, `--column`. **Write:** `-d`, `--delete`, `-f`, `--force`, `-a`, `--annotate`, `-s`, `--sign`, `-u`, `--local-user`, `-m`, `--message`, `-F`, `--file`, or a positional operand with no listing flag present.

`git tag -v` verifies a signature and only reads. It is not the verbose flag that `-v` means for `git branch` and `git remote`. Classifying it as a write would deny a command the gate allows today.

A positional operand triggers write **only when no listing flag is present**. Without that condition, `git branch --contains <commit>` and `git tag -l 'v1.*'` would misread their flag operands as a create.

For all four migrated entries and these three, an unrecognized form classifies as **write**, not uncertain. These are known write-capable commands whose read forms are the exception, so the safe default inverts. An unenumerated read form therefore produces a denial, which is recoverable, rather than a bypass, which is not.

**Add no read verb without evidence.** `git-tools` exists to codify a small set of sanctioned flows. It does not exist to mirror git's command surface. An earlier draft proposed vendoring the 41 commands that `git --list-cmds=list-plumbinginterrogators,list-ancillaryinterrogators` reports. That is scope this project cannot justify: no recorded operation needs `rev-list`, `for-each-ref`, `verify-commit`, `shortlog`, `name-rev`, `diff-tree`, or `show-ref`.

Exactly one addition has a real, verifiable need today:

- **`git merge-base`** — `dat-single-workspace-plans-and-programs` design §6 item 1 requires `git merge-base <integration> <build-branch>` from a primary checkout, to compute a `resign --base`. That document names the denial and states the gap must close first. It is a cited consumer requirement, not a hypothetical.

Everything else waits. The fail-closed default denies an unmodeled read verb, which is the safe direction, and the denial itself is the evidence that the verb is needed. That is how FB5, A4, and A1 each surfaced. The pattern is slow, but it is correct and it keeps the allowlist honest.

**No generator either.** `verbs.json` is `go:embed`-ed and the repo has no `go:generate` anywhere, so a generator would introduce a new pattern for one table. Its output would vary with the git version of whoever ran it, which makes a committed artifact nondeterministic unless CI pins a git version. The repo already has the right guard: extend `TestDefaultVerbs_CriticalVerbsPresent` (`adversarial_test.go:218`).

**Priority note:** A4 is a prerequisite for `dat-single-workspace-plans-and-programs` §6 item 1. The resign-gate swap cannot proceed until A4 lands.

### Acceptance criteria for Milestone A

Each fix must satisfy all three conditions.

1. Add a `want_deny:false` case to `decide-bash-corpus.json` for the over-block the fix removes. A5 has no over-block, so instead it adds a `want_deny:false` case per preserved read form (`git remote -v`, bare `git branch`, bare `git tag`).
2. Add a `want_deny:true` companion case proving the fix turned no genuine write into an allow. For A5 that means one case per write form listed in the split above.
3. Reconcile every fixture that pins the current behavior. A1 flips `devnull-redirect-is-no-blanket-allow-at-primary-cwd`. A2 flips the A21 pin and rewrites both A21 comments.
4. Rework `TestDefaultVerbs_CriticalVerbsPresent` (`adversarial_test.go:218`). It asserts five git entries against `verbs.json` today — `git commit` and `git add` in `WritePrefixes`, and `git status`, `git diff`, `git log` in `ReadPrefixes` (`adversarial_test.go:229-237`). All five fail once git leaves the file. Remove them and re-express the same guard against the new in-code git sets, so an accidental removal of `git merge-base` or of a split entry still fails a test. `TestDefaultVerbs_ShippedArtifactIsPopulatedAndValid` stays green, because 15 non-git read prefixes and every write class survive the removal.

The genuine-write anchors that must stay green are `TestClassifyBash_WritesAlwaysTrip` (`bash_test.go:36-58`), including `echo hi > file.txt`.

## 2 — Milestone B: relative-path resolution (FB8, criticality 12)

### B1 — the `worktree add` post-condition

`internal/cli/worktree.go:15-36`: `worktreeRegisteredAt` resolves the caller's `<path>` through `filepath.Abs` (`worktree.go:28`), that is, against the **process cwd**. It then compares the result against the repo-rooted absolute paths that `git worktree list` reports. A relative `<path>` matches only when the process cwd equals the `--repo` working tree.

`worktree add` therefore creates the worktree correctly, then fails its own post-condition at `worktree.go:92-95` with exit 90 and code `internal.git.worktree_add_unverified`. FB8 records three reproductions across two repos.

**Fix:** resolve a relative `<path>` against the resolved `--repo` working tree. That base is provably correct. The git library runs every command with `sysops.Options{Dir: r.Dir}`, and its own doc states "Every method on Repo runs relative to Dir, never the calling process's working directory". Git resolved the relative path against `r.Dir` when it created the worktree, so the check must use the same base. The evidence lives in the external module `claude-shared-tooling/go/git@v0.1.0/repo.go:11-13,47`, not in a local `internal/git/repo.go` — an earlier draft of this document cited the wrong path. The fix itself is entirely local: `repo` is in scope at the call site (`worktree.go:92`). Thread `repo.Dir` into `worktreeRegisteredAt` and join before resolving.

**Also fix the doc comment.** `worktree.go:12-14` already claims that a caller-supplied relative path "still matches what `git worktree list` reports". That claim is false. The comment must not outlive the bug.

**Also fix the triage text**, which actively misleads. It says "retry; if this persists, file an issue." Retrying cannot succeed. The second attempt fails differently, on an already-existing path. An operator who follows the message concludes the worktree does not exist, and starts cleaning up something that is fine.

**Why B1 matters beyond annoyance:** `worktree add` is the sanctioned entry point to a worktree-mandatory workflow. Any caller that gates on the exit code treats a good worktree as a failed one. The prescribed remedy then fails differently. This project's own predecessor hit exactly that confusion while landing.

### B2 — the same bug, second site, different repo

`claude-shared-tooling/go/git@v0.1.0/worktree.go:32` — the `DryRun` pre-check in `WorktreeAdd` calls `os.Stat(path)`, also process-cwd-relative, while the real add in the same function resolves through `r.Dir`. A `--dry-run` with a relative path from any other cwd returns a wrong existence answer.

**This site is outside `git-tools`.** It lives in the external module required by `go.mod`. The local checkout is `ai-shared-lib`. The GitHub repository name is `claude-shared-tooling`. Both names refer to the same repository.

Fix `git-tools` first. File B2 separately as a declared publish-then-consume hand-off. Do not pull it into this project's `file_surface`. That is precisely the cross-repo plan shape the companion design argues against.

### Audit result: no third site

The `design-architect` audit examined every `filepath.Abs`, `os.Stat`, `os.Getwd`, and `EvalSymlinks` call in non-test `internal/` code. `worktree.go:28,32` is the only place a caller-supplied path resolves against the process cwd for a repo-scoped operation. `config.go:105` probes for a config file and is not repo-scoped. `hooks.go:80` resolves a path already built from `RepoDir`. `push.go:202` and `scan.go:169,187` correctly pass `sysops.Options{Dir: dir}`.

**Conclusion: B1 is the only site inside `git-tools`. B2 is the only external site. There is no third.**

## Key decisions & tradeoffs

- **Re-pin scope** — options: track the `governance-git` re-pin as an in-plan milestone, or declare it a hand-off. **Chosen: declared hand-off.** Why: the design claims single-repo scope. A re-pin milestone would edit files in a different repo and break that claim. B2 is handled the same way, so the boundary stays consistent.
- **A2, the A21 deferral** — options: honor the prior deferral and drop A2, or reverse it. **Chosen: reverse it and fix.** Why: A1 and A2 refine the same predicate in opposite directions. Fixing one and deferring the other leaves the predicate incoherent. The cost is one predicate change plus two comment rewrites.
- **git classification shape** — options: anchor `WritePrefixes` as a cheap interim, or classify git in code by resolved subcommand. **Chosen: classify in code, reusing `gitSubcommand`.** Why: anchoring fixes A4 but only partly fixes A3, and leaves the duplicate-logic root cause that produced both. The helper already exists at `decide.go:434`.
- **Read-verb list growth** — options: vendor git's 41 published read-only commands, generate the set at build time, or add only verbs with recorded evidence. **Chosen: evidence only. `git merge-base` is the single addition.** Why: `git-tools` codifies sanctioned flows and does not reinvent git. No recorded operation needs the other 40. Vendoring them is unjustified surface, and the fail-closed default already makes a missing verb produce a safe denial that documents the real need. A generator would additionally add a new repo pattern for one table and make a committed artifact depend on the builder's git version.
- **A5, the three under-blocks** — options: fold into the A3 and A4 refactor, track as a separate defect, or defer. **Chosen: fold into A3 and A4.** Why: the refactor rewrites `git remote`, `git branch`, and `git tag` anyway. Carrying them across unchanged would knowingly preserve a gate bypass.
- **Where the git sets live** — options: restructure `verbs.json` to key on subcommands, or move the git sets into code beside `gitSubcommand`. **Chosen: move into code.** Why: `Verbs` is a flat `[]string` struct (`bash.go:22-38`). A flat prefix list cannot express "this subcommand under any global option", which is precisely what A3 needs. Reworking the JSON schema is a larger change than relocating twenty entries. Tradeoff accepted: git verbs move from a reviewable data artifact into Go source. They lose JSON-level auditability, and they leave the populated-class net of `TestDefaultVerbs_ShippedArtifactIsPopulatedAndValid`. The reworked `TestDefaultVerbs_CriticalVerbsPresent`, in acceptance criterion 4, is the replacement guard.
- **`git stash list` and `git stash show`** — options: add read forms, or keep `stash` as write. **Chosen: keep as write.** Why: A4 identifies the collision correctly, but no recorded operation needs either command. Subcommand classification reaches the same denial by a correct route. Adding an unevidenced read form is the sprawl this project refuses.
- **A1 fix shape** — options: reorder `ReadPrefixes` ahead of `writesFile`, or exempt discard targets and narrow the operand scope. **Chosen: exempt and narrow.** Why: the reorder would let `echo hi > file.txt` through, because `echo` is a read prefix.

## Constraints & dependencies

- **The gate blocks its own repair session.** A1 and A3 deny ordinary read commands during the build. Agents must avoid `2>/dev/null` and prefix every Bash call with `cd <worktree> &&`. The friction is real and was hit twice while writing this document.
- **`verbs.json` is `go:embed`-ed** (`verbs.go:9`). Any change to it requires a rebuild, not a config reload.
- **The release ships two binaries.** `.github/workflows/release.yml` cross-compiles the `git-tools` CLI and the `worktree-gate` binary for four os and arch cells.
- **A4 gates other work.** `dat-single-workspace-plans-and-programs` §6 item 1 waits on it.
- **FB1 should land first** (§0), so the planning front door works from any session tier.

## Risks

- **A fix turns a genuine write into an allow.** This is the one failure mode that matters, because the gate is a safety control. Mitigation: every fix carries a `want_deny:true` companion case, and `TestClassifyBash_WritesAlwaysTrip` must stay green. **Forces pause: yes.** A red genuine-write test must halt the build for human review.
- **A subcommand split misses a write form, or the migration drops one.** This is A5's own failure mode repeating, and it is the single largest risk in the project. It applies to the three defect splits (`remote`, `branch`, `tag`) and equally to the four migrated ones (`worktree`, `config`, `reflog`, `stash`), where a mistake would newly allow `git worktree add` or `git config --set` in a primary checkout. Mitigation: enumerate every write form explicitly, default all seven entries to write when the form is unrecognized, and add a `want_deny:true` corpus case per write form. **Forces pause: yes**, on any regression here.
- **The evidence-only rule slows real work.** A read verb nobody predicted produces a denial mid-task. Mitigation: accept it. The denial is safe, self-documenting, and cheap to fix in the next release. This is a deliberate trade against allowlist sprawl.
- **The A21 reversal contradicts a judgment made with context this project lacks.** Mitigation: record the reversal and its rationale in both comment sites, so a later reader sees a decision rather than an accident. **Forces pause: no.**
- **The consumer re-pin is forgotten after the release.** The build lands, the release ships, and no consumer picks it up. This is exactly FB9's coupling hazard. Mitigation: the hand-off is written into §3 with its full file list, not left implicit.
- **Line references in this document drift before the build starts.** Mitigation: every task re-verifies its cited line before editing.

## 3 — Sequencing and the release hand-off

1. **FB1 (§0)** — outside this project. Do it first. It unblocks the planning front door.
2. **A3, A4, and A5** — one change to git classification. A4 gates the companion design's resign-swap work. A5 closes a live gate bypass, so it must not slip to a later release.
3. **A1 and A2** — one change to the shared redirect predicate. A1 is the highest-frequency friction observed.
4. **B1** — independent of Milestone A. It can run in parallel inside the same build.
5. **The `git-tools` release** — one bare `vX.Y.Z` tag. `release.yml` cross-compiles both entry points for four os and arch cells.
6. **The `governance-git` re-pin** — a declared hand-off, in a different repo, after the release. Bump the `n="vX.Y.Z"` constant in `hooks/bootstrap-worktree-gate.sh` and in `hooks/pretooluse-worktree-gate.sh`. Refresh every per-os-and-arch row for both binaries in `data/binary-digests.json`. A test asserts that all three agree. Leave the `contract-digests` set untouched, because this project changes no contract artifact.
7. **B2** — a separate cross-repo hand-off against `ai-shared-lib` (`claude-shared-tooling`), after `git-tools` lands.

Minimizing releases matters precisely because step 6 is multi-file and cross-repo. That is FB9's lesson.

## Open questions

None block derivation. All three questions the seed brief raised are now settled and recorded under Key decisions and tradeoffs. The audit in §2 closed the third question with a concrete result.

One question remains open for the operator, and does not block this build:

- Should the `git-tools` CLI grow sanctioned read verbs, so agents stop reaching for raw `git` and the gate stops needing a verb allowlist at all? This is a larger architectural change and is out of scope here. Record it as a future direction.

## Readiness record

This document passed five `design-architect` CRITIQUE rounds. Each round verified every claim against source rather than accepting the text.

| Round | Verdict | What it found |
| --- | --- | --- |
| 1 | NEEDS-WORK | Release scope breached the single-repo thesis. A1 and A2 acceptance criteria were unsatisfiable against pinned fixtures. The A1 root-cause framing invited a fix that would let a genuine write through. |
| 2 | NOT-READY | The refactor silently required splits for `worktree`, `config`, `reflog`, and `stash` that no section listed. The document contradicted itself on whether git classification lives in code or in `verbs.json`. |
| 3 | NEEDS-WORK | Removing git from `verbs.json` breaks five assertions in `TestDefaultVerbs_CriticalVerbsPresent`. The write-default would newly deny `git branch -a`, `git branch -v`, and `git tag -l`. |
| 4 | NEEDS-WORK | `git tag -v` means verify and reads. It was listed as a write, which would deny a command allowed today. `git config --get-all` is denied today, so listing it as read added a sixth behavior change. |
| 5 | **READY** | Confirmed both round-4 fixes landed with no surviving restatement. Verified `git tag -u` is a genuine write. Found no new contradiction against criteria 8, 10, and 11, or against acceptance criterion 2. No findings. |

Every finding across the five rounds is applied. Round 5 is the confirming round and returned READY with an empty findings list.

Two claims in this document were measured, not reasoned. The A5 table came from calling `classifyPiece` against the shipped `verbs.json`. The round-2 critique reproduced every row independently. The `worktree add` arity in criterion 12 was read from `internal/cli/worktree.go:60-62`.

## Reconciliation note

Three feedback items (FB1, FB2, FB8) were unaddressed and untracked in the `git-governance-emergency-followup` register after that project's whole-plan acceptance. Each was checked against the `dat-single-workspace-plans-and-programs` thesis rather than assumed to fit.

| Item | Root cause | Same as the single-workspace thesis? |
| --- | --- | --- |
| FB1 | A model-roster data gap (`claude-opus-5` carries no `cross_family_rank`) plus an error message that does not name its own remedy | No — model-tiering data and error text, a different product surface |
| FB2 | A gate classification defect: a redirect flips a read command to write-class, and then every operand is judged a write destination | No — and it falsified an early draft of that design's Evidence F, which had misattributed this defect as a cross-repo-reading problem. Corrected there. Not re-litigated here. |
| FB8 | A relative path resolved against the process cwd instead of the repo working tree, in a post-condition check | No — a thematic echo only. Evidence B is also a path-identity problem, but it is a different product and different code. |

None of the three shares the single-workspace-plan root cause. FB2 and FB8 cluster tightly with each other and with three defects already known in this repo — FB4, FB5, and the merge-base collision found during the predecessor's landing. One repo, two layers. That clustering is why this is one project with two milestones rather than three separate projects.
