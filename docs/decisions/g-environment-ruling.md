---
name: Work item G — ruling on the two git-tools environment variables
description: "Work item G's prerequisite ruling for the gate-consolidation port: git-tools removes both GITTOOLS_REPO (internal/cli/config.go:292) and GIT_TOOLS_BETTERLEAKS_BIN (internal/cli/scan.go:52), with one testable port acceptance each, the no-environment-policy rule for the ported classifier, the re-measured classifier-file grep, and what stays open."
id: project:git-tools-gate-consolidation:g-environment-ruling
tags:
  - type:decision
  - topic:git-governance
  - topic:tooling
  - team:psa
  - status:complete
  - privacy:public
  - owner:public
links:
  - project:git-tools-gate-consolidation:design
  - project:git-tools-remediation-brief:workitem-g
updated: 2026-09-03T00:00:00Z
---

# Work item G — ruling on the two git-tools environment variables

## The ruling

| Variable | Read at | What it selects | Ruling |
| --- | --- | --- | --- |
| `GITTOOLS_REPO` | `internal/cli/config.go` line 292 | the repository every verb acts on | **Remove** |
| `GIT_TOOLS_BETTERLEAKS_BIN` | `internal/cli/scan.go` line 52 | the credential-scanner binary the scan execs | **Remove** |

Neither variable is exempted, and neither is constrained. Both reads go away before the port lands.

The reason is one sentence, and it is the same for both. The port moves the classifier into the process that reads them, so that process becomes a gate — and a gate must take no input, target or policy, from a source the governed party can write.

## Why this ruling exists

The design records the risk this ruling answers: consolidation lands the classifier in the `git-tools` CLI process, and that process reads two environment variables today. Work item G rules on both before any port work starts.

LED-090 states the principle. A gate whose policy input the governed party controls is not a gate. Move policy out of the process environment, into a source the model cannot write.

## GITTOOLS_REPO — remove

**What it does.** `repoDirForConfig` (`internal/cli/config.go` lines 286 to 296) resolves the acting repository in flag-beats-env-beats-default order: the `--repo` flag, then `GITTOOLS_REPO` at **line 292**, then `"."`. Its answer is used twice. Line 237, inside `loadConfigForDir`, assigns it over `Config.Repo` after every config layer resolves, so it is the CLI's only answer to which repository a verb writes to. Line 169 passes the same answer as the directory the implicit `git-tools.yaml` is resolved against, so the variable also picks which policy file loads.

**Why remove.**

- **The codebase already adopted this rule, and this read is the surviving exception.** Commit `88c23a9` established that no non-argv layer may select the acting repository: a config file's own `repo` key is overwritten at line 237, pinned by `TestLoadConfig_ConfigFileCannotSelectTheRepo` (`internal/cli/config_test.go` line 184). The environment is the one non-argv layer that still selects it.
- **The classifier cannot see it.** `sc15Retargets` (`worktree-gate/detect/decide.go` line 1455) treats `--repo` as the only retarget channel, and `gitToolsDestinations` (line 755) reads only `--repo`'s value as a destination. That function's own comment at line 743 names `GITTOOLS_REPO` as part of the answer set the classifier does not inspect. The classifier reads no environment at all, by design and by test.
- **One spelling is already closed; the other is not.** An inline prefix (`GITTOOLS_REPO=/elsewhere <path> merge main`) fails `sc15Identity` (`decide.go` line 1179): `sc15IdentityCause` line 1222 requires the piece's leading token to equal the argv-supplied provisioned binary path, and an assignment token is not that path — so it denies. An **ambient** exported variable does not fail it. A clean invocation by the provisioned absolute path satisfies identity, names no retargeting flag, and then acts on a repository the gate never saw and never judged. That is the exact outcome the config-file fix existed to prevent.
- **Removal breaks no provisioned workflow.** No hook, bootstrap script, plugin, or settings file in this fleet sets `GITTOOLS_REPO`; a grep across `plugins/` and `.claude/` in the marketplace repository finds it only in this project's own planning documents. Inside git-tools it appears at the read itself, in four doc comments, and in the one test subtest below. `--repo` remains, and covers every legitimate use.

**Cost, named.** `internal/cli/config_test.go` lines 232 to 252 currently pin the opposite: that `GITTOOLS_REPO` selects the repository, and that `--repo` beats it. That subtest inverts. The change itself is one site — deleting the read at line 292. The `GITTOOLS_*` koanf layer (lines 200 to 202) still parses a `repo` key, and line 237 already overwrites it, so no second removal is needed.

## GIT_TOOLS_BETTERLEAKS_BIN — remove

**What it does.** `resolveBetterleaksPath` (`internal/cli/scan.go` line 51) reads `GIT_TOOLS_BETTERLEAKS_BIN` at **line 52** and returns that path whenever `usableBetterleaksBinary` accepts it. Failing that, it falls back to `siblingBetterleaksPath` (line 84), and returns `errBetterleaksUnconfigured` (line 42) only when neither source resolves. The resolved binary is what the mandatory credential, PII, and financial scan execs — the scan that gates `merge`, `push`, `rebase`, and `tag create`.

**This matches LED-090's binary-substitution shape.** LED-090's measured set names "a variable substituting an arbitrary gate binary." This is that variable. It substitutes the binary whose verdict gates every write verb, an environment-set value always wins over the provisioned one, and `usableBetterleaksBinary` (line 70) checks only that the path is a regular file carrying an executable bit — no digest, no provenance. A stub that exits clean therefore turns every landing scan green while the command still reports as scanned.

**Why remove rather than constrain.**

- **The provisioned path already comes from a source the model cannot write.** `siblingBetterleaksPath` finds the binary next to the running `git-tools` executable — the plugin-data `bin` directory, provisioned at bootstrap, and denied as a write target by the config-write gate regardless of basename (`config_write_gate.py` lines 32 to 39). That is precisely the substitution LED-090 asks for: the environment out, an unwritable source in.
- **The fallback already carries production traffic.** `bootstrap-worktree-gate.sh` line 359 records that git-tools v1.10.0 and later fall back to the sibling binary automatically when the export does not happen. Removing the read costs no coverage.
- **Constraining it needs machinery git-tools does not have.** A digest constraint would need the expected digest, and the digest table lives in the plugin, not in this repository. Passing it on argv would add a second trusted-input surface to solve a problem the sibling lookup already solves with none.
- **Removal is order-independent with the plugin.** `bootstrap-worktree-gate.sh` line 356 exports the variable today. Once the read is gone, that export is inert rather than broken, so the plugin-side deletion can land later, in the marketplace repository, on its own schedule.

**Cost, named.** The variable is load-bearing across the `internal/cli` suite, in two distinct ways.

- **In-package tests that pin the environment's precedence.** `scan_betterleaks_sibling_internal_test.go` lines 24 to 45 (`TestResolveBetterleaksPath_EnvVarWinsOverSibling`) inverts outright. `scan_test.go` lines 119 to 156 drive `resolveBetterleaksPath` through the variable and need re-expressing against the sibling.
- **Subprocess suites that use it to install a stub scanner.** `integration_test.go`'s `TestMain` sets it for the whole package run (line 41), and `merge_scan_test.go`, `merge_scan_config_test.go`, and `secret_scan_categorized_severity_test.go` override it per test. These drive the built CLI, so a Go-level seam does not reach them. They move to the pattern `scan_betterleaks_sibling_test.go` already uses: `copyCLIToPrivateDir` copies the built binary into a private `t.TempDir()`, and the stub is planted next to it as a file named `betterleaks`.

`resolveBetterleaksPath` also has to gain the injected self-lookup that `siblingBetterleaksPathFrom` already offers (`scan.go` line 92). Its absence is already recorded — `scan_betterleaks_sibling_internal_test.go` lines 20 to 23 note that `resolveBetterleaksPath` has no injected seam of its own, and plant a file next to the real test binary to work around it. Adding the seam is part of the removal, not a follow-up.

## The port's acceptances

One testable acceptance per variable. Both belong to the port tasks, and both are ordinary Go tests in package `cli` under `internal/cli`.

**G-1, `GITTOOLS_REPO`.** With `GITTOOLS_REPO` set to a directory that is not the process working directory, and no `--repo` flag given, `loadConfig` resolves `Config.Repo` to `"."`. With `--repo` given, `Config.Repo` is the flag's value. The environment never selects the acting repository. `TestLoadConfig_ConfigFileCannotSelectTheRepo`'s subtest at lines 232 to 252 inverts into this.

**G-2, `GIT_TOOLS_BETTERLEAKS_BIN`.** With `GIT_TOOLS_BETTERLEAKS_BIN` set to a usable executable that is not the sibling binary, `resolveBetterleaksPath` returns the sibling path when a usable sibling resolves, and `errBetterleaksUnconfigured` when none does. It never returns the environment's path. `TestResolveBetterleaksPath_EnvVarWinsOverSibling` inverts into this.

## The rule for the ported classifier

**The ported classifier reads no environment variable for policy.** Neither `internal/gate/branch` nor `internal/gate/configwrite` may call `os.Getenv`, `os.LookupEnv`, or `os.Environ` for any decision input. Every input a verdict depends on arrives on argv or on stdin.

This is a rule the codebase already enforces and already knows how to pin. `worktree-gate/detect/decide_sc15_residual_test.go` lines 60 to 79 prove the SC15 allowance is reached through argv alone, with a recording environment asserting zero queried keys; lines 115 to 125 are the compile-out companion, asserting that `decide.go`'s own source names no environment-read primitive. The ported packages carry the same pair of pins.

## What the classifier files actually read

A grep of the four classifier files for the git-tools environment namespace (`GIT_TOOLS_*`, `GITTOOLS_*`) finds **one variable, `GIT_TOOLS_TAG`** — and it is not an input. (The wrappers also read `CLAUDE_PLUGIN_DATA`, outside that namespace and only as the cross-check the table records below.)

| File | Lines | Policy read from the environment | Other environment reads |
| --- | --- | --- | --- |
| `git-gate.sh` | 872 | none | none |
| `pretooluse-worktree-gate.sh` | 158 | none | `CLAUDE_PLUGIN_DATA` (lines 120, 122, 123) |
| `config-write-gate.sh` | 91 | none | `CLAUDE_PLUGIN_DATA` (lines 76, 78, 79) |
| `config_write_gate.py` | 502 | none | none |

`GIT_TOOLS_TAG` is not an environment input. `pretooluse-worktree-gate.sh` line 47 assigns it as a hardcoded version pin (`v1.10.0`), and lines 114, 128, and 130 read that assignment. Nothing lets the environment supply it.

`CLAUDE_PLUGIN_DATA` is read only to be cross-checked, never trusted. Both wrappers derive the plugin-data root from their own resolved `$0` path, then compare the variable against it; unset or disagreeing fails closed, and the derived root — not the variable — is what the gate then uses. Setting it can stop a gate from allowing. It cannot make one allow.

`config_write_gate.py` states the same rule about its own inputs, in its own text: the plugin-data root is passed in by the wrapper and derived from the wrapper's resolved path, "never read from an environment variable here" (lines 35 and 473).

So LED-090's measured set does not survive re-measurement on the classifier side. The environment surface the consolidation has to worry about is the two CLI reads this ruling closes, and nothing else in the four files.

## Scope, and what stays open

**The rest of work item G ships as a separate effort.** This plan carries only G's ruling on the two `git-tools` environment variables, because the design names that ruling as the port's prerequisite. Nothing else from work item G is in this plan.

This plan leaves open:

- **The rest of LED-090's measured set.** The brief names a merge carve-out on both gates, a variable that disables the entire worktree gate, and a variable that sets the tracking-doc exemption root. This ruling re-measured only the four classifier files and the two CLI reads. Whether those three exist anywhere else is not settled here.
- **The rest of the `GITTOOLS_*` config-env layer.** `internal/cli/config.go` lines 200 to 202 map *every* `GITTOOLS_*` variable onto a `Config` key, so the environment can still set `privacy_tier`, `privacy_marker_exempt`, `secret_scan_exempt`, `secret_scan_extra_rules`, `secret_scan_extra_allowlist`, and `secret_scan_categorized_severity` — scan policy, in LED-090's exact shape. This ruling covers the two variables it was asked to rule on and does not touch that layer. It is the largest environment surface left in the process the port lands in, and work item G's own effort should rule on it next.
- **A positive policy source.** This ruling removes two environment reads. It does not build the source-the-model-cannot-write that LED-090 asks policy to move *into*.
- **Work item G's other entries** — LED-065 (wrapper constructs bypass both gates), LED-064 (the landing channel deadlock), and LED-066 (a fast-forward push to a protected ref is ungated).
- **The plugin-side export.** Deleting `GIT_TOOLS_BETTERLEAKS_BIN` from `bootstrap-worktree-gate.sh` line 356 is marketplace work. The removal here does not require it, and does not schedule it.
