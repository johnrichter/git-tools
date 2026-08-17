---
name: "worktree-gate vs the incumbent write-locus gate"
description: "Design record of the three substantive differences between worktree-gate/detect (deny-capable PreToolUse gate on Write|Edit|Bash) and governance-git's incumbent write-locus gate (Write|Edit only, ask on uncertainty), each annotated with its blast radius."
id: "doc:git-tools:worktree-gate-vs-incumbent"
tags: [type:doc, topic:process, status:complete, privacy:public, owner:public]
links: []
updated: 2026-07-29T00:00:00Z
---

# worktree-gate vs the incumbent write-locus gate

The incumbent (`governance-git`'s `write-locus-gate.sh`) gates Write|Edit against a curated
code-source-extension allowlist and a `.claude/worktrees/` path-segment check, asking on
uncertainty. `worktree-gate/detect` supersedes it for the worktree-isolation invariant with
three substantive changes.

## 1. Bash added as a conservative over-approximation

The incumbent has no position on Bash at all: a shell command that mutates a tracked file
(`sed -i`, a redirect, an invoked editor, `git commit`) bypassed it entirely. `ClassifyBash`
closes that gap by treating any command that isn't confidently a read as a possible write.

**Blast radius:** every Bash call now carries gate risk. An unrecognized tool or an exotic
shell construct that used to run unexamined can now deny outside a worktree even when
harmless -- new false-positive surface on Bash specifically, traded for closing a real bypass.

## 2. No source-extension filter -- every file path is in scope

The incumbent only gates a curated set of programming/scripting extensions; `.md`, `.json`,
`.yaml`, and other config/doc formats pass through it untouched regardless of worktree
membership. This gate applies the same rule to any Write/Edit target, extension-agnostic.

**Blast radius:** doc/data/config edits that were previously silent no-ops under the incumbent
now deny outside a worktree. Broader protection for tracking docs, config, and generated data
files sharing a repo with source, at the cost of more friction on non-code edits at the primary
checkout.

## 3. No-repo/uncertain case moves from ask to deny (SC-WORKTREE)

The incumbent's "no git repo found" and "repo root indeterminate" cases both resolve to `ask`,
giving the operator an interactive out. This gate treats the same cases as fail-closed `deny`.

**Blast radius:** removes the interactive escape hatch. Eliminates the risk of a reflexive
approval on an ask-prompt landing an unisolated write, at the cost of hard-blocking a
legitimate-but-unclassifiable call that previously could proceed after confirmation -- the
operator must resolve the ambiguity (e.g. create the worktree) rather than override it.

## Packaging defects deny, except where the verdict was never in question

A missing or corrupt classifier artifact (`verbs.json`) denies rather than allows: the defect
could be masking a real write, so it's treated the same as any other signal this gate can't
resolve confidently (fail closed). The one exception is a call already resolved independently of
the artifact -- e.g. a Bash call confirmed to run inside a worktree needs no classification at
all to allow. There, the defect is surfaced as a loud diagnostic (`Decision.Degraded`) without
changing the verdict, since there was never a verdict for it to change.
