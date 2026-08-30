# D4: tag create hardening — report

## Status

Done. All three gaps closed additively; `tag create`'s no-`-m`-flag behavior
(the derived tag name is still the tag's own message) and every existing
acceptance case are unchanged.

## Files touched

- `internal/cli/tag.go` — precondition check + post-creation verify/rollback,
  plus updated `--help` Long text and exit-code table.
- `internal/cli/tag_test.go` — two new tests (no-key refusal, verify-failure
  rollback), plus one new `os` import.
- `internal/cli/tag_call_site_test.go` — new file, the single-call-site lint.

## Fix 1 — precondition check before signing

Before `git tag -s` runs, `tag create` now calls
`signing.NewProber(&git.Repo{Dir: "."}).Available(ctx)` — the same prober
helper `internal/signing/signing.go` already exposes and `merge.go` already
uses for its own merge-commit signing precondition. No new signing-detection
logic was written.

- Probe execution failure (couldn't even run the probe) → `finishErr`,
  code `internal.git.signing_probe_failed`.
- Probe ran but no key/agent resolved → `finishDiagnostic` with
  `clikit.NewPreconditionUnmet`, code `precondition_unmet.git.signing_key_unresolved`
  (exit 30), message naming the actual git-reported cause (the prober's
  `detail`), advice to configure a signing key. Reuses the exact code name
  `merge.go` already uses for the analogous check, for consistency across
  the module.

This replaces the previous behavior where a signing failure fell through to
`git tag -s`'s own nonzero exit, reported generically as
`internal.git.tag_create_failed`.

## Fix 2 — verify with `git tag -v`, roll back on failure

Placement: `pushRef` runs last in `tag create`'s `RunE` (confirmed by
reading the existing control flow — `return pushRef(...)` was the final
statement). The verify step is inserted directly after the `git tag -s`
call and before that `pushRef` call, so a failure here never needs to touch
a remote — only the local tag `tag create` just made.

- `git tag -v <name>` fails to execute → `finishErr`,
  code `internal.git.tag_verify_failed`.
- `git tag -v <name>` exits nonzero (signature does not verify) →
  `git tag -d <name>` deletes the local tag, then `finishErr` with code
  `internal.git.tag_signature_unverified` and a message stating the tag
  "failed its own post-creation signature verification and was rolled
  back", including git's own verify-failure detail.
- If the rollback delete itself also fails, the error message says so
  explicitly and gives the exact manual `git tag -d <name>` to run — the
  one case this task's rollback cannot self-heal.

Exit code: reused the existing `internal` (90) family, matching this file's
own existing precedent of treating a nonzero exit from a `tag.go`-owned git
subcommand as `internal` rather than inventing a new status class (the
sibling `git tag -s` nonzero-exit handling a few lines above already does
this for tag creation itself).

### Test forcing the failure path

`TestTagCreate_FailedPostCreationVerification_RollsBackAndRefuses` uses
`signingRepo` (ssh-format signing, already used elsewhere in this suite),
then points `gpg.ssh.allowedSignersFile` at a second, unrelated key pair's
public key. `git tag -s` never reads `allowedSignersFile`, so signing still
succeeds; `git tag -v` does read it, so verification fails deterministically.
The test asserts: exit 90/`internal`, the message names both the
verification failure and the rollback, no local tag survives, and nothing
reached the bare remote.

## Fix 3 — single-call-site lint

`internal/cli/tag_call_site_test.go`, package `cli` (internal test package,
matching the existing `TestNoWorktreeRemoveCallSitePassesForce` convention
in `worktree_test.go`, whose exported-within-package `extractCalls` helper
this test reuses directly rather than duplicating it).

`TestRawGitTagCallSite_ConfinedToTagGo` walks every non-test `.go` file in
the module (skipping `.git`, `.task-reports`, `.dat`), extracts every
`gitexec.RunGit(...)` call's balanced-paren argument text, splits each on
its top-level commas (a new small helper, `splitTopLevelArgs`, since the
existing `extractCalls` only isolates the call's argument text, not its
individual arguments), and flags any call whose subcommand argument is the
literal `"tag"`. It asserts every such call site's containing file is
`internal/cli/tag.go`.

Note on scope: the task described this as a check that the literal
substring `git tag` appears in exactly one place. A literal substring check
does not fit the actual source shape — `gitexec.RunGit(ctx, ".", "tag", ...)`
never spells the two words together, while plenty of comments and this
verb's own `--help` text legitimately do (in `tag.go` itself, in `push.go`,
and in `worktree-gate/detect/decide.go`'s raw-command classifier, which
recognizes attempted `git tag` invocations for denial and is not a call
site at all). The lint instead targets actual `git tag` subcommand
invocations — the thing D1 migrated away from everywhere but `tag.go` — and
confines them to that one file rather than requiring exactly one
invocation, since this task's own fix 2 legitimately adds two more
raw-`tag`-subcommand calls (`-v`, `-d`) alongside the pre-existing `-s`,
all three belonging to the same verb and the same file.

Ran standalone and confirmed passing before and after fixes 1–2 landed
(3 call sites now, all in `tag.go`; 0 elsewhere).

## Test results

- `go build ./...` — pass, no output.
- `go vet ./...` — pass, no output.
- `go test ./...` -- timeout 30m` — see below (internal/cli alone runs
  ~20 minutes by design, per this task's own briefing; not a hang).
