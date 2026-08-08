---
name: git-tools — Gate Classification Layer + Relative-Path Resolution
description: "git-tools fix project, one repo, two milestones. Milestone A: four worktree-gate classification defects (FB2/FB4/FB5 + a merge-base write-prefix collision). Milestone B: a cwd-vs-repo relative-path bug (FB8) in worktree add. FB1 (plan-with-team roster gap) excluded -- fix directly first, bootstrap blocker."
id: project:git-tools-gate-classification-and-path-resolution:design
tags:
  - type:project
  - topic:tooling
  - topic:git-governance
  - team:psa
  - status:draft
  - privacy:internal
  - owner:operator
links:
  - project:git-governance-emergency-followup:design
  - project:git-governance-emergency-followup:feedback-review-record
  - project:dat-single-workspace-plans-and-programs:design
updated: 2026-08-08T17:00:00Z
---

# git-tools — Gate Classification Layer + Relative-Path Resolution

**Status: draft seed design brief.** Produced by the `magistrate` agent during `git-governance-emergency-followup`'s post-build AAR, from defects reproduced first-hand while landing that build and reconciled against the companion `dat-single-workspace-plans-and-programs` design. Nothing here is implemented. Every claim below was verified against source at the versions cited; re-verify before acting, since line references drift.

**Scope: one repo — `git-tools`.** Two layers, one landing, one release, one `GIT_TOOLS_TAG` bump. The single-repo scope is deliberate, per `dat-single-workspace-plans-and-programs`.

## Reconciliation note (why this is a separate project, not folded into `dat-single-workspace-plans-and-programs`)

Three feedback items (FB1, FB2, FB8) were found unaddressed and untracked in `git-governance-emergency-followup`'s register after that project's whole-plan acceptance. Checked each against the single-workspace-plan thesis rather than assuming a fit:

| Item | Root cause | Same as the single-workspace thesis? |
| --- | --- | --- |
| FB1 | Model-roster data gap (`claude-opus-5` has no `cross_family_rank`) + an error message that doesn't name its own remedy | No — model-tiering data and error UX, a different product surface entirely |
| FB2 | Gate classification defect: a `/dev/null` redirect flips a read command to write-class, then every operand is judged a write destination | No — and it actually **falsified** an early draft of that design's Evidence F, which had misattributed this exact defect as a cross-repo-reading problem. Corrected there; not re-litigated here. |
| FB8 | A relative path resolved against the process cwd instead of the repo working tree, in a post-condition check | No — thematic echo only (Evidence B is also a path-identity problem), different product, different code, not fixed by that proposal |

None of the three shares the single-workspace-plan's root cause. FB2 and FB8, however, cluster tightly with each other and with defects already known in this same repo (FB4, FB5, and a merge-base write-prefix collision found this session) — one repo, two layers. That clustering is why this is one project with two milestones, not three separate ones: FB9 (from the same predecessor project) already showed that splitting a `git-tools` release into multiple passes creates its own coordination hazard (the `GIT_TOOLS_TAG` re-pin must move in lockstep across consumers) — minimizing the number of releases is a real risk reduction, not just convenience.

## 0 — Explicitly excluded: FB1

FB1 (`plan-with-team` Phase 0 roster hard-stop) is **not** part of this project. It lives in `ai-shared-lib` (roster data + tests) and the marketplace plugin (the exit-3 message), shares no code with anything here, and is a **bootstrap blocker**: `plan-with-team` cannot plan *any* project from a non-Opus session until it is fixed. Routing it through `plan-with-team` is circular. Fix it directly, first, as a standalone change, outside project machinery:

1. `ai-shared-lib/go/roster/model-roster.json` — set `models["claude-opus-5"].cross_family_rank = 6`. Rank 6 is the vacant slot between `opus-4-8` (5) and `fable-5` (7) in the existing sequence. **Operator ratification required**: `cross_family_rank` is an operator ruling by design, not a vendor fact, so 6 is a high-confidence inference from the existing sequence plus the recorded "Opus above Sonnet" ruling — not a mechanical derivation. Also extend `_cross_family_order.note` (it names `opus-4-6/4-7/4-8` above Sonnet but omits `opus-5`) and bump `_cross_family_order.as_of` and `_as_of`.
2. Re-point the two tests that enshrine the gap onto a still-unranked example — `claude-opus-4-5`/`claude-sonnet-4-5`, the deliberate allowlist-parity carries whose vendor-fact fields are null by design: `roster/roster_test.go::TestCompareCrossFamilyMissingRankErrors` (currently `Compare(opus-5, fable-5)`) and `roster/adversarial_test.go::TestCompareOneSideDeclaredOneUndeclaredErrors` (currently `Compare(opus-5, sonnet-5)`). `TestCompareUnknownIDPropagatesStaleNotDefaultOrdering` uses `opus-5` only as the *known* side and is unaffected.
3. Regenerate `ai-shared-lib/go/anthropic-specifications.json` from the updated roster.
4. **Add the recurrence guard** (the half FB1's own proposed solution omits, and the half that matters): refuse a model row with sourced vendor facts but no rank. Checkable invariant: `knowledge_cutoff != null ⇒ cross_family_rank != null`, which distinguishes `opus-5` (the bug) from `opus-4-5`/`sonnet-4-5` (the intentional null-rank carries, whose vendor-fact fields are also null). Without this guard, the next model added reproduces FB1 exactly.
5. `plan-with-team`'s Phase 0 exit-3 message must name the one-step operator remedy ("switch the session model to an Opus tier and re-run"), not only roster regeneration, which is a maintainer action. Do **not** add a hardcoded fallback — the skill's own instructions forbid it, and `Compare`'s refuse-unless-both-declared invariant is correct and should stay as-is.

Item 1 alone unblocks the front door; items 4-5 are what stop it recurring and stop it reading as "the skill is broken." Blocking assessment: hard stop, trivially worked around (switch to Opus 5), badly signposted (the message points at a maintainer action, not the operator's one-step fix).

## 1 — Milestone A: the gate classification layer

**Surface:** `worktree-gate/detect/{bash.go,decide.go,verbs.json}` + `testdata/decide-bash-corpus.json`. Four defects, one layer, one corpus. They compose — fix together, not in four separate passes, or fixes risk contradicting each other on the same predicate.

### A1 — FB2, redirect-flips-read-class (criticality 16, reproduced 6× across this session)

`classifyPiece` (`bash.go:469-471`) returns `ClassWrite` on `p.writesFile` **before** consulting `ReadPrefixes`. A `2>/dev/null` on a modeled read command therefore makes the whole piece write-class; `namedPaths`' `default` branch (`decide.go:328-333`) then returns every non-flag operand as a candidate write destination, so a search pattern, a glob, or a read path is judged a write target.

Proof, reproduced directly: `ls <path>` — allowed. `ls <path> 2>/dev/null` — denied, naming `<path>` as the write target.

*Proposed fix, narrowest first:* recognize `/dev/null` as a discard target that can never be a repo write — it is the single most common shell idiom and this one change removes most of the observed friction. Then, per FB2's own proposed solution, restrict operand-as-destination to commands actually modeled as writers, judging an unmodeled command by its redirect targets alone. Order matters: the `/dev/null` fix is small, safe, and independently valuable; the operand-scope change is the real fix and needs corpus work to land safely.

### A2 — FB4, redirect target classification (criticality 4)

`isFdDupTarget` treats a bare digit as an fd-dup, so a redirect to a file literally named `2` raises no write signal. Same redirect-classification surface as A1 — fix together or the two changes will contradict each other. Note the asymmetry the pair reveals: A1 is a false *positive* (a discard read as a write) and A2 a false *negative* (a real write read as a discard), and both live in the same predicate — a strong reason to review them as one change, not two.

### A3 — FB5, leading git global options (criticality 4)

`read_prefixes`/`write_prefixes` anchor on a bare `git <subcommand>`, so `git -C <dir> status` matches no read prefix, classifies `ClassUncertain`, and is denied though read-only.

### A4 — write-prefix loose-match collision (found this session, distinct from FB5)

`WritePrefixes` match via unanchored `strings.HasPrefix` while `ReadPrefixes` require a word boundary (`bash.go:29-31, 480-497`). `git merge` is a write prefix, so read-only **`git merge-base`** classifies as a write and is denied. Same class: `git stash list`, `git stash show`. Related, from the anchored-read side plus an incomplete allowlist: `git diff-tree`, `git show-ref`, `git config --get-regexp`, and unmodeled read-only plumbing (`git rev-list`, `git for-each-ref`, `git verify-commit`, `git shortlog`, `git name-rev`).

*Proposed fix for A3+A4 together:* classify `git` by resolved subcommand token — split argv, skip global options (`-C`, `-c`, `--git-dir`, `--work-tree`), match the subcommand against explicit read/write sets. Interim, cheaper step: anchor `WritePrefixes` at a word boundary the way `ReadPrefixes` already are, and add the missing read-only plumbing verbs to the allowlist.

**Priority note:** A4 is a **prerequisite for `dat-single-workspace-plans-and-programs` §6 item 1**, which requires `git merge-base <integration> <branch>` from a primary checkout to compute a `resign --base`. The resign-gate swap proposed there cannot proceed until A4 lands.

**Cross-cutting acceptance criterion for Milestone A:** every fix must add its reproducer to `testdata/decide-bash-corpus.json` — all four defects are classification-table gaps, so a corpus entry is the only durable guard against regression. Also worth an explicit criterion: no fix may turn a genuine write into an allow — A2 is the reminder that this layer's failure modes run in both directions, and the corpus should assert both a false-positive and a false-negative case per fix.

## 2 — Milestone B: relative-path resolution (FB8, criticality 12)

### B1 — CLI post-condition

`internal/cli/worktree.go:15-36`: `worktreeRegisteredAt` resolves the caller's `<path>` with `filepath.Abs`, i.e. against the **process cwd**, and compares against `git worktree list`'s repo-rooted absolute paths. A relative `<path>` matches only when the process cwd happens to equal the `--repo` working tree, so `worktree add` **creates the worktree correctly and then fails its own post-condition** with exit 90 (`internal.git.worktree_add_unverified`). Reproduced three times across two repos per FB8's own record.

*Fix:* resolve a relative `<path>` against the resolved `--repo` working tree. This is provably the correct base: the library runs git with `sysops.Options{Dir: r.Dir}` (`repo.go:47`), so git itself resolved the relative path against `r.Dir` when it created the worktree. `repo` is already in scope at the call site (`worktree.go:92`) — thread `repo.Dir` into `worktreeRegisteredAt` and join before resolving.

*Also fix the triage text*, which actively misleads: it says "retry; if this persists, file an issue," but retrying **cannot** succeed — the second attempt fails differently, on an already-existing path. An operator following the message concludes the worktree does not exist and starts cleaning up something that is fine.

### B2 — the same bug, second site, different repo (new finding; not in FB8's original text)

`claude-shared-tooling/go/git@v0.1.0/worktree.go:32` — `WorktreeAdd`'s dry-run pre-check uses `os.Stat(path)`, also process-cwd-relative. `--dry-run` with a relative path from any other cwd returns a wrong existence answer.

**This site is outside `git-tools`**, so it is the one tail that leaves this project's single-repo scope. Either fix `git-tools` first and file B2 separately against `ai-shared-lib`, or declare it an explicit publish-then-consume hand-off if a future program-level rollup exists to carry it. Do not silently pull it into this project's `file_surface` — that is precisely the cross-repo plan shape the companion design argues against.

**Why B1 matters beyond annoyance:** `worktree add` is the sanctioned entry point to a worktree-mandatory workflow. Any caller gating on the exit code treats a good worktree as a failed one, and the prescribed remedy then fails differently — this project's own build hit exactly this shape of confusion during landing.

## 3 — Sequencing

1. **FB1 (§0)** — outside this project, do first, unblocks the planning front door for everything downstream including this project's own planning session.
2. **A4** then **A1** — A4 gates the resign-swap work in the companion design; A1 is the highest-frequency friction observed (6 hits in one session).
3. **A2, A3** — same layer, batch with the above.
4. **B1** — independent of Milestone A; can run in parallel within the same build.
5. **B2** — separate, cross-repo, after `git-tools` lands; scope as its own hand-off, not folded in.

One release at the end: one tag bump, one hook re-pin, per FB9's lesson about re-pin coupling being itself a hazard worth minimizing.

## 4 — Open questions for `plan-with-team`

1. Should `read_prefixes`/`write_prefixes` remain data (`verbs.json`) once `git` is classified by resolved subcommand, or does `git` move to code with the table kept for non-`git` commands only?
2. Is there an authoritative list of read-only `git` plumbing verbs worth vendoring wholesale, rather than growing the allowlist one denial at a time? The current pattern — each gap discovered by a live denial during real work — is what produced FB5, A4, and A1 independently across different sessions.
3. Does any other `git-tools` command share B1's cwd-vs-`--repo` resolution defect? Both known instances (B1, B2) were found incidentally; a deliberate audit of every `filepath.Abs`/`os.Stat` call on a caller-supplied path against a `--repo`-scoped operation is warranted, since two independent sites already exist in two different repos.
