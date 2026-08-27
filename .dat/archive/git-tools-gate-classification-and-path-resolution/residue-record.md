---
name: git-tools-gate-classification-and-path-resolution — Residue Record
description: "Post-archive verification (SC-F4) of the superseded gate-classification-and-path-resolution design: each of its 14 criteria re-checked against the current tree and marked met-by-P2, a disposition line per feedback.json entry with an OQ3 duplication check, and the FB6-requested FB4/FB6 reconciliation with harness-followup."
id: project:git-tools-gate-classification-and-path-resolution:residue-record
tags:
  - type:record
  - topic:git-governance
  - topic:tooling
  - team:psa
  - status:complete
  - privacy:public
  - owner:public
links:
  - project:git-tools-gate-classification-and-path-resolution:design
  - project:git-governance-gates-and-signing:design
  - project:git-tools-merge-signing-and-release-provenance:design
  - project:harness-followup:register
updated: 2026-08-27T18:09:54Z
---

# Residue record — git-tools-gate-classification-and-path-resolution

Written for SC-F4 of `git-tools-merge-signing-and-release-provenance` (design D8/D9, OQ3), at the point the superseded design moves from `git-tools/.dat/git-tools-gate-classification-and-path-resolution/` to `git-tools/.dat/archive/git-tools-gate-classification-and-path-resolution/`.

**The chain, verified rather than assumed.** This design never had its own `plan.json` or execution record (checked: `git log --all --diff-filter=A --name-only -- '.dat/git-tools-gate-classification-and-path-resolution/*'` returns only `design.md`, `feedback.json`, `feedback.md`). It went from a READY design straight to being absorbed as **source material** — its own §S1 (the 14 criteria) and §S1b (FB1-FB7) — into a *different*, already-complete project: `git-governance-gates-and-signing` (`marketplace/.dat/archive/git-governance-gates-and-signing/`, `status:complete`, 31 tasks done, published `git-tools v0.2.0`). That project's own plan built the fixes, task by task — `M1.P2.T2`, `M2.P1.T1`, `M2.P2.T1`, `M2.P2.T2`, `M2.P2.T3`, `M2.P4.T1`, each `status:done` in its `execution.json`. `git-tools-merge-signing-and-release-provenance` (the project commissioning this record) is a *later* effort still, landing on top of `git-governance-gates-and-signing`'s output — its own milestones start at M3, exactly where `gates-and-signing`'s M1/M2 leave off. That design's own text shorthands the absorbing project **P2** (D9: "P2's `M2.P1.T1` reclassified the command as a write"; §D: "P2's reviewer measured 1 of 8 binaries reproducing"), and those citations resolve to `gates-and-signing`'s own `v0.2.0` release and `FB27`/`FB20` entries. **P2 = `git-governance-gates-and-signing` — not the project commissioning this record.**

So **every verdict below is evidence-based** — re-derived by reading the current source and running the current test suites (D8) — not read off either project's plan or execution record, though `git-governance-gates-and-signing`'s own `execution.json` corroborates several rows below (cited inline where it does).

**Method (D8 — verify before archiving):** each of the 14 criteria was checked against the source at `HEAD` in this worktree, not against either design's own narration. `go build ./...` and `go test ./...` both ran clean from repo root before this record was written.

**Verdict: all 14 criteria are met-by-P2** (P2 = `git-governance-gates-and-signing`, the project whose own plan absorbed and fixed this design's 14 criteria and its open feedback items — see the chain above). None required a "superseded" verdict — nothing in the original 14 was overridden or invalidated by later work; each is independently true today, confirmed by source citation and a passing test.

---

## 1 — The 14 success criteria (design `:54-67`)

| # | Criterion (design `:54-67`) | Verdict | Evidence locus (verified against current tree) |
| --- | --- | --- | --- |
| 1 | `git status 2>/dev/null` from a primary checkout is allowed | met-by-P2 | `worktree-gate/detect/bash.go:369-385` (`isFdDupOrDiscardTarget`, `/dev/null` exempted at `:381`); corpus case `devnull-redirect-is-no-blanket-allow-at-primary-cwd`, `worktree-gate/detect/testdata/decide-bash-corpus.json:669-673`, `want_deny:false` |
| 2 | `find <path> -name '<glob>' 2>/dev/null` from a primary checkout is allowed | met-by-P2 | Same mechanism as #1; corpus case `a1-find-devnull-redirect-from-primary-cwd-allowed`, `decide-bash-corpus.json:675-679`, `want_deny:false` |
| 3 | `echo hi > file.txt` remains denied outside a worktree; `TestClassifyBash_WritesAlwaysTrip` stays green | met-by-P2 | `worktree-gate/detect/bash_test.go:36-58` (`echo hi > file.txt` at `:42`); `go test ./worktree-gate/detect/... -run TestClassifyBash_WritesAlwaysTrip` passes as part of the full-suite run below |
| 4 | `tool run >1` classifies as a write (A21 deferral reversed) | met-by-P2 | `worktree-gate/detect/bash.go:369-385` (dup-capable operator gate on `isFdDupTarget`); `worktree-gate/detect/bash_test.go:97-101` pins `{"tool run >1", true, true}` |
| 5 | `git -C <dir> status` is allowed | met-by-P2 | `worktree-gate/detect/decide.go:540-558` (`gitSubcommand` skips `-C`), `:587-615` (`classifyGit`); corpus case `git-leading-dashC-read-classified-allowed-at-primary`, `decide-bash-corpus.json:701-705`, `want_deny:false` |
| 6 | `git merge-base <a> <b>` is allowed | met-by-P2 | `worktree-gate/detect/decide.go:566-571` (`gitReadSubcommands["merge-base"] = true`); corpus case `git-merge-base-is-read-allowed-at-primary`, `decide-bash-corpus.json:713-717`, `want_deny:false` |
| 7 | `git remote add`, `git branch -D`, `git tag -d` denied outside a worktree | met-by-P2 | `worktree-gate/detect/decide.go:617-732` (`classifyGitRemote`, `classifyGitBranch`/`classifyGitRefEditFn`, `classifyGitTag`); corpus cases `git-remote-add-write-denied:743`, `git-branch-force-delete-write-denied:791`, `git-branch-delete-write-denied-fb4:797`, `git-tag-delete-write-denied:833`, all `want_deny:true` |
| 8 | Enumerated read forms of `remote`/`branch`/`tag` stay allowed | met-by-P2 | `worktree-gate/detect/decide.go:636-647` (`gitBranchListFlags`, `gitTagListFlags`); all fourteen enumerated read-form cases carry `want_deny:false` in `decide-bash-corpus.json`: `:725` `git-remote-bare-read-allowed`, `:731` `git-remote-verbose-read-allowed`, `:737` `git-remote-show-read-allowed`, `:749` `git-branch-bare-read-allowed`, `:755` `git-branch-all-read-allowed`, `:761` `git-branch-remotes-read-allowed`, `:767` `git-branch-verbose-read-allowed`, `:773` `git-branch-list-read-allowed`, `:779` `git-branch-show-current-read-allowed`, `:785` `git-branch-contains-read-allowed`, `:809` `git-tag-bare-read-allowed`, `:815` `git-tag-list-pattern-read-allowed`, `:821` `git-tag-annotations-read-allowed`, `:827` `git-tag-verify-is-read-allowed` — interleaved in the same corpus block (`:719-839`) with the four write-form denials cited in row 7, not a uniformly-false span |
| 9 | Every git verb classifies the same way with or without a leading global option (`-C`, `-c`, `--git-dir`, `--work-tree`, `--namespace`) | met-by-P2 | `worktree-gate/detect/decide.go:540-558` (`gitSubcommand` skip list); dedicated test `TestClassifyGit_LeadingGlobalOptionParity`, `worktree-gate/detect/adversarial_test.go:284-315`, sweeping all 8 global-option forms across 16 verbs |
| 10 | Migration changes no classification other than the five defects (`worktree`, `config`, `reflog`, `stash` preserved) | met-by-P2 | `worktree-gate/detect/decide.go:734-758` (`gitWorktreeReadSubcommands`, `gitReflogReadSubcommands`, `classifyGitSubSelect`, `classifyGitConfig`); corpus cases `decide-bash-corpus.json:851-941` (`git-worktree-list-retargeted-into-primary-read-allowed`, `git-worktree-add-write-denied`, `git-config-get-read-allowed`, `git-config-list-read-allowed`, `git-config-positional-set-write-denied`, `git-config-unset-write-denied`, `git-config-add-write-denied`, `git-config-get-regexp-still-denied`, `git-config-get-all-still-denied`, `git-reflog-show-read-allowed`, `git-reflog-expire-write-denied`, `git-stash-write-denied`, `git-stash-list-still-write-denied`, `git-stash-pop-write-denied`) |
| 11 | No read verb added without a recorded denial or cited consumer requirement; `git merge-base` is the only addition | met-by-P2 | `worktree-gate/detect/decide.go:566-571` (`gitReadSubcommands`, single map, `merge-base` the one addition beyond the pre-existing `status`/`diff`/`log`/`show`/`fetch`/etc.); `TestDefaultVerbs_CriticalVerbsPresent`, `worktree-gate/detect/adversarial_test.go:234-282`, pins `merge-base` read / `merge` write and every split verb's read+write pair |
| 12 | `git-tools worktree add <relative-path> <ref> --repo <dir>` from any process cwd returns exit 0 | met-by-P2 (citation re-verified, see note) | `internal/cli/worktree.go:13-27` (`worktreeRegisteredAt` resolves the caller's path against `repoDir`, the repository working tree, not the process cwd) calling `internal/worktreeclean/worktreeclean.go:299` (`ResolvedPath`) |
| 13 | `decide-bash-corpus.json` carries, per fix, a `want_deny:false` case for the removed over-block and a `want_deny:true` companion proving no genuine write became an allow | met-by-P2 | Per-fix pairs, each independently checked: A1 — `:669-673` (`devnull-redirect-is-no-blanket-allow-at-primary-cwd`, false) / `:681-685` (`a1-devnull-alongside-real-write-denies`, true). A2 — `:297-302` (`a2-bare-digit-redirect-target-denies-from-primary-cwd`, true) / `:303-308` (`a2-genuine-fd-dup-ampersand-one-stays-allowed-at-primary`, false). A3/A4 — `:701-705` (`git-leading-dashC-read-classified-allowed-at-primary`, false) / `:707-711` (`git-leading-dashC-write-classified-denied-at-primary`, true); `:713-717` (`git-merge-base-is-read-allowed-at-primary`, false) / `:719-723` (`git-merge-is-write-denied-at-primary`, true). A5 — the fourteen read cases in row 8 (`:725-839`, false) paired with the four write cases in row 7 (`:743`, `:791`, `:797`, `:833`, true). The four migrated verbs' (`worktree`/`config`/`reflog`/`stash`) own preserved-behavior pairs at `:845-941` satisfy criterion 10's no-other-regression requirement rather than this criterion's per-fix pairing, since preserving existing behavior is not "the over-block the fix removes" |
| 14 | The full `worktree-gate` and `git-tools` test suites pass | met-by-P2 | `go build ./...` clean; `go test ./...` from repo root: `ok` for `internal/cli`, `internal/gitexec`, `internal/hooks`, `internal/result`, `internal/signing`, `internal/worktreeclean`, `worktree-gate/detect`, `worktree-gate/fixtures`, `worktree-gate/lifecycle` (run at this record's `HEAD`, 2026-08-12) |

### Note on criterion 12's citation

The design that seeded SC-F4 (`git-tools-merge-signing-and-release-provenance/design.md`, D8) cited `internal/cli/worktree.go:32-38` as criterion 12's fix. **That citation has drifted and no longer resolves to the fix.** The fix itself landed under `git-governance-gates-and-signing`'s own plan as `M2.P2.T1` ("B1 — fix worktree add's relative-path post-condition", within merge commit `72494c56fc6c8c8a52920756545a2c3406f60f70`). This repo's own later `M3.P1.T3` ("Extract `internal/worktreeclean` with no behavior change", commit `393bcec`) then pulled the path-resolution helper out of `worktree.go` and into `internal/worktreeclean/worktreeclean.go`, shifting lines. Lines `32-38` in the current tree hold the unrelated `worktreeEntry` struct. This is exactly the failure mode D8 warns against — a citation asserted rather than re-checked — so this row cites the re-verified current locus (`worktree.go:13-27` plus `worktreeclean.go:299`) instead of repeating the stale one. The underlying fact the citation supports — `worktree add`'s relative-path base is the repo working tree, not the process cwd — still holds.

---

## 2 — Disposition of every `feedback.json` entry

OQ3 asks whether this design's `feedback.json` duplicates a live entry elsewhere. The check below is against `marketplace/.dat/harness-followup/feedback.json`, the standing catch-all register for findings with no project home — the one place a duplicate would most likely surface.

| ID | Title | Disposition | OQ3 — duplicate found elsewhere? |
| --- | --- | --- | --- |
| FB1 | plan-with-team self-declared readiness at the round cap | Open, out of scope for `git-tools`. A `plan-with-team` skill-procedure defect, not a `git-tools` code defect; no file in this repo implements the round-cap check. No evidence found that it has landed anywhere. | No duplicate found in `harness-followup` (its own FB1 is an unrelated `git-tools merge` false-success defect) |
| FB2 | The gate under repair obstructs its own repair session | Closed as evidence, per its own `proposed_solution` ("No change beyond shipping this project"). The two cited denials that are still-open defects — `find … 2>/dev/null` (A1) and `git -C <dir> remote -v` (A3) — are fixed; see criteria 1-2 and 5 above (the `-C … remote -v` compound specifically is swept by criterion 9's global-option parity test). The `sed -i` denial it also cites is correct behavior (`sed -i` is a genuine write), not a defect | No duplicate found in `harness-followup` |
| FB3 | design-architect lacked local-checkout-to-remote-name mapping | Open, out of scope for `git-tools`. An agent-dispatch/process defect (design-architect prompting), not `git-tools` code. The design's own text says it was "folded into `dat-single-workspace-plans-and-programs`" — a different repo/project this record cannot verify | No duplicate found in `harness-followup` |
| FB4 | A5 bypass demonstrated live: `git branch -d` succeeded from the primary checkout | Closed. Fixed by `git-governance-gates-and-signing`'s own `M2.P1.T1` ("A3/A4/A5 — classify git in code by resolved subcommand", commit `113a865`, 2026-08-10). See criterion 7 above; the corpus even carries a case named for this entry, `git-branch-delete-write-denied-fb4` (`decide-bash-corpus.json:797`, `want_deny:true`) | See the dedicated FB4/FB6 reconciliation in §3 — `harness-followup` does not duplicate this finding, but its FB6 is directly related |
| FB5 | The gate cannot reach zero worktrees: `worktree remove` is not a sanctioned verb | Closed. `git-governance-gates-and-signing`'s own `M2.P2.T3` ("The worktree-remove fork — standalone verb, merge cleanup flags, one shared cleanup", commit `6d22aa7`) sanctioned `worktree remove` as one of the CLI's landing verbs. Current evidence: `worktree-gate/detect/decide.go:949-959` (`sc15VerbAllowed` lists `worktree remove` among the six sanctioned verbs) | No duplicate found in `harness-followup`. Related but distinct: `harness-followup` FB10 records a later, different defect — the gate intermittently refusing `worktree add`/`remove` mid-session — not a re-statement of FB5's original exclusion claim |
| FB6 | Correction to FB5: the gate can reach zero worktrees, from outside the repo | Closed — an informational correction, not a defect requiring its own fix. The ergonomics gap both FB5 and this entry point at was closed by the same `M2.P2.T3` fix recorded under FB5 above | No duplicate found in `harness-followup`. **Not to be confused** with `harness-followup`'s own FB6 (see §3) — different register, different subject |
| FB7 | Over-block: the gate denies deleting untracked, gitignored scratch state | Closed. `git-governance-gates-and-signing`'s own `M2.P2.T2` ("B4 — FB7 worktree-home exemption", commit `11cc747`) added the exemption. Current evidence: `worktree-gate/detect/decide.go:333-369` (worktree-home scratch exemption logic) and `worktree-gate/detect/fb7_worktree_home_scratch_sdet_test.go` (dedicated regression suite) | No duplicate found in `harness-followup` |

---

## 3 — The FB6-requested reconciliation: two registers, two FB4s, two FB6s

This is the reconciliation `harness-followup`'s FB6 asks for ("The two accounts should be reconciled by whoever owns that register"). There are **two separate feedback registers in play**, and both an FB4 and an FB6 collide across them by number only:

| Register | FB4 | FB6 |
| --- | --- | --- |
| `git-tools-gate-classification-and-path-resolution/feedback.json` (this design, now archived) | "A5 bypass demonstrated live: `git branch -d` succeeded from the primary checkout" — recorded 2026-08-08T22:19:31Z, during that project's own cleanup | "Correction to FB5: the gate can reach zero worktrees, from outside the repo" — about `worktree remove`, unrelated to branch deletion |
| `marketplace/.dat/harness-followup/feedback.json` (standing register) | "The LFS fixture guard still lists four deleted build-helpers binaries…" — unrelated to branch deletion or the gate's git classification at all | "Branch deletion has no sanctioned route from the primary checkout" — records `git -C <primary-checkout> branch -d <name>` being **refused** on 2026-08-11 |

**The reconciliation:** gate-classification's FB4 (`git branch -d` **succeeded**, 2026-08-08) and harness-followup's FB6 (`git branch -d` **refused**, 2026-08-11) both hold — they are not in conflict — because `git-governance-gates-and-signing`'s own `M2.P1.T1` (commit `113a865`, landed 2026-08-10T21:39:38Z) sits between the two observations in time and reclassified `git branch` from a flat read prefix to a subcommand-resolved verb where `-d`/`-D` are writes (see criterion 7 above). That is the same `M2.P1.T1` design D9 shorthands as "P2's" (§1's chain above resolves P2 to `git-governance-gates-and-signing`). The sequence is:

1. 2026-08-08 — gate-classification's FB4: `git branch -d` allowed (the A5 bypass, pre-fix).
2. 2026-08-10 — `git-governance-gates-and-signing`'s `M2.P1.T1` lands the A3/A4/A5 reclassification.
3. 2026-08-11 — harness-followup's FB6: `git branch -d` refused (post-fix, correctly denied).

Both accounts are accurate for the point in time each was recorded. Neither register needs a correction; the fix that separates them is now on record here.

**`harness-followup` also holds an FB4**, and it is a **different finding** from gate-classification's FB4: it is about a stale LFS fixture-guard allowlist (four deleted `build-helpers` binaries left in a test's fixture list), with no connection to branch deletion or to the gate's git-command classification. The two FB4s share only their numeric ID, by virtue of each register numbering its own entries from 1 independently.

harness-followup's FB6 raises one item this reconciliation does not close: **`git-tools` still has no CLI `branch` verb**, so raw `git branch -d` is the only route to delete a branch and it is (correctly) denied from a primary checkout outside a worktree. That gap is tracked as its own item in `git-tools-merge-signing-and-release-provenance`'s design (D9, SC-C7) and is out of this record's scope — this record closes only the FB4/FB6 sequencing question harness-followup's FB6 asked for.

---

## Provenance

- Source: `git-tools/.dat/archive/git-tools-gate-classification-and-path-resolution/design.md` (criteria at `:54-67`) and `feedback.json`/`feedback.md` (FB1-FB7), moved unchanged from `git-tools/.dat/git-tools-gate-classification-and-path-resolution/`.
- Cross-repo source for §1's P2 attribution: `marketplace/.dat/git-tools-merge-signing-and-release-provenance/design.md` (D8, D9, §D — the citations that shorthand `git-governance-gates-and-signing` as "P2") and `marketplace/.dat/archive/git-governance-gates-and-signing/design.md` (§S1/§S1b, the absorption record) and its `execution.json` (tasks `M1.P2.T2`, `M2.P1.T1`, `M2.P2.T1`, `M2.P2.T2`, `M2.P2.T3`, `M2.P4.T1`, all `status:done`) — read for cross-check only, not modified, not part of this repo's `file_surface`.
- Cross-repo source for §3: `marketplace/.dat/harness-followup/feedback.json` (FB4, FB6), read for reconciliation only — not modified, not part of this repo's `file_surface`.
- Verification performed 2026-08-12 in this worktree: `go build ./...`, `go test ./...`, and direct line citations against `worktree-gate/detect/{bash,decide}.go`, their test files, `worktree-gate/detect/testdata/decide-bash-corpus.json`, and `internal/cli/worktree.go` / `internal/worktreeclean/worktreeclean.go`.
