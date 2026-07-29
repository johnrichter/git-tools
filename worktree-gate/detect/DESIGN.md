---
name: "worktree-gate vs the incumbent write-locus gate"
description: "Design record of the four substantive differences between worktree-gate/detect (deny-capable PreToolUse gate on Write|Edit|Bash) and governance-git's incumbent write-locus gate (Write|Edit only, ask on uncertainty), each annotated with its blast radius."
id: "doc:git-tools:worktree-gate-vs-incumbent"
tags: [type:doc, topic:process, status:complete, privacy:public, owner:public]
links: []
updated: 2026-07-29T00:00:00Z
---

# worktree-gate vs the incumbent write-locus gate

The incumbent (`governance-git`'s `write-locus-gate.sh`) gates Write|Edit against a curated
code-source-extension allowlist and a `.claude/worktrees/` path-segment check, asking on
uncertainty. `worktree-gate/detect` supersedes it for the worktree-isolation invariant with
four substantive changes.

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

## 4. Two allow paths restored, narrowly, so retirement can't regress the incumbent

Widening scope to Bash and to every file class (items 1-2) closes real gaps, but the incumbent
also carried two ALLOW behaviors this gate lacked outright. Each is restored as a data-driven,
pinned-to-its-exact-case override rather than a general loosening.

**Tracking-doc exemption.** A Write/Edit whose target sits at any depth under the configured
project dir (`CLAUDE_PROJECT_DIR`) and whose basename is in the delivery-agent-team tracking-doc
set (`design.md`, `plan.json`, `plan.md`, `execution.json`, `execution.md`, `feedback.json`,
`feedback.md`) is allowed even in a primary checkout -- matching the incumbent's `$PROJ`
carve-out. The basename set ships as `trackingdocs.json`, a data artifact next to `verbs.json`,
not a literal in `decide.go`. The check runs before the repo-root walk and is extension-agnostic
by construction: this gate has no source-extension filter to begin with, so the exemption applies
to the basename alone. **Fail direction:** a missing or corrupt `trackingdocs.json` fails open and
loud (`Decision.Degraded`) for any call the exemption could have covered (target under the
configured project dir) -- never a deny. A call outside that scope is unaffected by the defect,
since the exemption could never have applied to it regardless.

**Sanctioned-landing-merge override.** With `DAT_MERGE_GATE=1`, a Bash `git merge` or `git commit`
whose cwd is a primary checkout is allowed -- build-with-team's documented landing flow
(`git merge --no-ff <branch> -m ...`) run directly from the primary checkout. The incumbent never
gated Bash at all, so this exact pattern was never blocked before; the override exists so
superseding it doesn't newly block a sanctioned flow. It is pinned narrowly: the whole command,
after splitting on the same shell connectors `ClassifyBash` uses, must reduce to exactly one piece
that is itself `git merge` or `git commit` at a word boundary. Any connector (`&&`, `;`, `|`,
newline) disqualifies it by producing more than one piece; a subshell or an env-var-prefixed form
disqualifies it too, since neither piece then starts with the exact verb text. A write-carrying
shell metacharacter inside the single piece -- a redirect (`>`/`<`), command or variable
substitution (`$(...)`, `${...}`, backticks), or a backgrounding `&` -- disqualifies it as well,
since those never split into a separate piece yet still carry a side effect the base classifier
would otherwise catch via `WriteContains`. So a non-covered write verb can never ride along. The
override is scoped to `KindPrimary` only and is independent
of classifier health, same as the existing worktree short-circuit. **Fail direction:** unset, or
any value other than exactly `"1"`, leaves the deny byte-identical to today's.

**Blast radius:** both paths are opt-in and narrowly scoped, so they don't reopen the
worktree-isolation invariant generally -- only the two named cases the incumbent already allowed.

## Unchanged: the packaging-defect exception

A missing or corrupt classifier artifact fails open and loud (see `doc.go`), never denies -- a
broken data file is never treated as a signal about the call being gated. The incumbent applies
the same fail-open rule to its own missing/corrupt extension-set data file, and the tracking-doc
basename set (item 4 above) follows the identical rule.
