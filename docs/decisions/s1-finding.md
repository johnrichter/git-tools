---
name: S1 finding — hooks.json reach and the decision-time verifier
description: "Probe result for git-tools-gate-consolidation's S1 success criterion (does a hooks.json command reach the plugin-data root?) and the resulting pick between the two bounded verification-wiring options."
id: project:git-tools-gate-consolidation:s1-finding
tags:
  - type:decision
  - topic:git-governance
  - team:psa
  - status:complete
  - privacy:public
  - owner:public
links: []
updated: 2026-09-03T00:00:00Z
---

# S1 finding — does a hooks.json command reach the plugin-data root, and which verifier wins

## Answer

A hooks.json command's spawned process **does** reach a variable that names the plugin-data root (`CLAUDE_PLUGIN_DATA`) — proven live below, not inferred from source comments. No hooks.json in this marketplace spells that variable today; every one spells `CLAUDE_PLUGIN_ROOT` instead.

That reachability makes Option B *mechanically* possible, but it does not make it the right pick. **Chosen: Option A — keep one small verifier script.** Reachability answers "can hooks.json's command line resolve the binary path itself"; it does not answer "does anything re-verify that binary's digest on every decision." Only Option A does. See Decision below.

## Probe 1 — static: does any hooks.json in this marketplace name `CLAUDE_PLUGIN_DATA`?

Command:

```
grep -rn "CLAUDE_PLUGIN_DATA\|CLAUDE_PLUGIN_ROOT" /home/bits/Development/workspaces/psa-platform/marketplace/plugins/*/hooks/hooks.json
```

Output: 37 matches across 16 `hooks.json` files, every one naming `CLAUDE_PLUGIN_ROOT`. Representative lines:

```
.../governance-git/hooks/hooks.json:9:   "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/git-gate.sh\""
.../governance-git/hooks/hooks.json:18:  "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/config-write-gate.sh\""
.../governance-git/hooks/hooks.json:27:  "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/pretooluse-worktree-gate.sh\""
.../governance-git/hooks/hooks.json:37:  "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/bootstrap.sh\""
.../governance-git/hooks/hooks.json:41:  "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/bootstrap-worktree-gate.sh\""
.../governance-git/hooks/hooks.json:45:  "command": "\"${CLAUDE_PLUGIN_ROOT}/hooks/charter.sh\""
... (16 plugins ship a hooks.json, 37 command entries total, every one CLAUDE_PLUGIN_ROOT)
```

`grep -c` per file confirms zero matches for `CLAUDE_PLUGIN_DATA` in any of the 16 files. This settles acceptance item 3: **no hooks.json in this marketplace names `CLAUDE_PLUGIN_DATA` today. Every one names `CLAUDE_PLUGIN_ROOT`.**

Grep alone answers "what's written today," not "what's reachable." The static result cannot tell us whether `CLAUDE_PLUGIN_DATA` would expand if a hooks.json command *did* name it — that needs a live probe, not a read of source.

## Probe 2 — live: does the process a hooks.json command spawns actually have `CLAUDE_PLUGIN_DATA` in its environment?

This session is itself governed by `governance-git`'s live `PreToolUse` hooks — `plugins/governance-git/hooks/hooks.json` fires `pretooluse-worktree-gate.sh` on every Bash/Edit call this task makes. That script (read as reference, not declared in this task's file surface) hard-requires the variable before doing anything else:

```
[ -n "${CLAUDE_PLUGIN_DATA:-}" ] || deny "governance-git: CLAUDE_PLUGIN_DATA is unset, so the plugin-data root cannot be cross-checked. Failing closed."
resolved_env_root=$(cd -- "$CLAUDE_PLUGIN_DATA" 2>/dev/null && pwd) || resolved_env_root=""
[ "$resolved_env_root" = "$plugin_data_root" ] || deny "governance-git: CLAUDE_PLUGIN_DATA ($CLAUDE_PLUGIN_DATA) disagrees with the plugin-data root derived from this hook's own path ($plugin_data_root). Failing closed rather than trusting either value alone."
```

If the harness did not populate `CLAUDE_PLUGIN_DATA` into that spawned process, or populated it with the wrong value, **every** Bash/Edit call in this task would be denied with one of those two messages. It never was, across dozens of calls this task issued. Two directly probed cases:

**Case 1 — a denial that fires, but for classification, never for `CLAUDE_PLUGIN_DATA`.** Command:

```
touch should-be-denied-test.txt   # run with cwd = the primary checkout, /home/bits/Development/workspaces/psa-platform/git-tools
```

Output:

```
worktree-gate: this command writes into the primary checkout of "/home/bits/Development/workspaces/psa-platform/git-tools" via "/home/bits/Development/workspaces/psa-platform/git-tools/should-be-denied-test.txt", not a worktree -- write to a path inside a worktree of the repository that contains this target instead; the provisioned `git-tools`, run by its absolute provisioned path, lists that repository's worktrees with `worktree list --repo <dir>`; if that is already the exact command this denied, then the binary at that path is missing or no longer matches the digest its plugin pins, so the gate has no sanctioned channel to grant it, and a fresh session re-provisions it
```

A second command in the same probe, an unresolvable write target, drew a parallel classification-specific denial:

```
echo foo > /tmp/probe-test-$$.txt
```

```
worktree-gate: "/tmp/probe-test-$$.txt" names a write target this gate cannot resolve statically, so it cannot rule out a primary checkout -- respell the target as a literal path, with no variable, glob, or `~` for the shell to expand, so the gate can resolve where the write lands
```

Both denials name the classification reason (primary-checkout write target, unresolvable target) and neither ever cites `CLAUDE_PLUGIN_DATA`. If that variable were unset or mismatched in the hook's own process, the script's own logic above guarantees the `CLAUDE_PLUGIN_DATA`-specific deny fires instead, before classification is even reached — it never did, in this probe or in any other Bash/Edit call this task made.

**Case 2 — reads succeed outright**, e.g. `git status`, `git log`, `grep`, `find`, `ls`, `cat` all returned real command output rather than a gate-denial JSON blob, across this entire task.

Contrast with this task's own tool-invocation shell, a *different* process the harness does not hand these variables to:

Command:

```
env | grep -i CLAUDE_PLUGIN
```

Output: (empty, exit 1 — no match)

So two distinct processes were probed: the Bash-tool subprocess this task runs commands in (`CLAUDE_PLUGIN_ROOT`/`CLAUDE_PLUGIN_DATA` absent, confirmed by grep), and the `PreToolUse` hook subprocess `hooks.json` spawns for every one of those commands (`CLAUDE_PLUGIN_DATA` present and correct, confirmed by the absence of either of `pretooluse-worktree-gate.sh`'s own `CLAUDE_PLUGIN_DATA` denial messages across every call, including the two denials captured above that fired for an entirely different reason). Corroborating filesystem evidence: the plugin-data root this script's `derive_plugin_data_root()` computes from `$0` — `<plugins-root>/data/governance-git-jr-claude-plugins` — exists on disk at `/home/bits/.cache/claude/plugins/data/governance-git-jr-claude-plugins/bin/`, holding exactly the binaries (multiple tagged `worktree-gate-*` builds up to `worktree-gate-v1.10.0`, `git-tools`, `betterleaks`) the script's derivation and `bootstrap-worktree-gate.sh`'s provisioning describe.

**Conclusion: a hooks.json command does reach a variable that names the plugin-data root.** `CLAUDE_PLUGIN_DATA` is live in the exact process `hooks.json`'s command line spawns — the same expansion point `CLAUDE_PLUGIN_ROOT` already uses in every marketplace `hooks.json` today. There is no mechanism by which one env var would expand there and the other wouldn't; both are plain shell variable expansions inside the same quoted `"command"` string, resolved by the same shell at the same spawn.

## Decision — Option A, keep one small verifier script

**Option A** — keep one small verifier script. `hooks.json` invokes a short wrapper (as `pretooluse-worktree-gate.sh` does today); the wrapper resolves the plugin-data root, looks up the pinned row in `binary-digests.json`, computes the live sha256 of the provisioned binary, and execs it **only on a match** — every time, on every `PreToolUse` decision.

**Option B** — register the provisioned binary path in `hooks.json` directly, e.g. `"command": "\"${CLAUDE_PLUGIN_DATA}/bin/git-tools\" hook pretooluse"`, no wrapper.

**Chosen: Option A.**

Reachability (Probe 2) makes Option B mechanically buildable — `hooks.json` could resolve `${CLAUDE_PLUGIN_DATA}/bin/git-tools` directly. But buildable is not the bar. Under Option B nothing stands between `hooks.json` and the binary: the digest check that `bootstrap-worktree-gate.sh` performs at `SessionStart` runs once per session, and a binary swapped in at that stable path *after* provisioning would run unverified on every subsequent decision until the next session start. That is a **drop of decision-time verification**, exactly what this plan's own key-tradeoffs record forecloses: "A binary does not prove its own identity, so a drop of decision-time re-verification stays out of scope." A self-verifying binary is not a fix for this — an artifact confirming its own bytes match its own bytes proves nothing about provenance; the check has to live outside the thing being checked.

Option A keeps that outside check. On every `PreToolUse` firing, the wrapper — not the binary — reads the pinned digest row from tracked plugin source (`binary-digests.json`), hashes the binary currently on disk, and only execs on a match. This is decision-time re-verification: the same call-by-call cadence `pretooluse-worktree-gate.sh` already performs for `worktree-gate` and (best-effort, for the SC15 landing-verb allowance) for the `git-tools` CLI today. Consolidating onto one binary changes what gets verified — one entry point instead of two — but not that it gets re-verified on every decision, which is what acceptance items 6 and 7 require and what the design's own tradeoff record already settled ahead of this probe.

## What task M3.P2.T1 changes under this option

M3.P2.T1 ("Rewire the plugin to the single binary") lands Option A's shape across these marketplace files (all under `plugins/governance-git/`, per its own declared file surface):

- `hooks/hooks.json` — the `PreToolUse` entry for Write/Edit/NotebookEdit/Bash still names a wrapper script (not the raw `git-tools` binary path), now the sole classifier entry point.
- `hooks/*.sh` — the wrapper script itself (successor to `pretooluse-worktree-gate.sh`) and `bootstrap-worktree-gate.sh`, updated to provision and verify one binary instead of two.
- `data/binary-digests.json` — repinned to the new git-tools tag, four git-tools rows, no worktree-gate row.
- `data/contracts/*.json` — the mirrored contract-digest copies re-synced to match, byte for byte.
- `hooks/tests/surface-hygiene/**`, `hooks/tests/git-gate/cwd-corpus.json`, `hooks/tests/worktree-gate/run.sh` — test surfaces updated for the one-binary wiring.

No file in this list points `hooks.json` at a bare provisioned-binary path with no intervening verification step — that would be Option B, not the option this finding picks.
