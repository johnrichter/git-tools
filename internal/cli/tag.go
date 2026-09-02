package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/git"
	"github.com/johnrichter/git-tools/internal/gitexec"
	"github.com/johnrichter/git-tools/internal/signing"
)

// versionPlaceholder is the token a --shape pattern uses to mark where a
// bare semver value goes: the exact placeholder "language-tools tag shape"
// prints in the pattern it derives from a module's declared tag_form
// (CONTRACT-LT-CONFIG) — "vX.Y.Z" for a root module, "<path>/vX.Y.Z" for a
// monorepo one. create never resolves or increments a version itself; it
// only substitutes one into this placeholder.
const versionPlaceholder = "X.Y.Z"

// bareVersion is what the placeholder stands for: three dot-separated
// non-negative integers, nothing else. --shape's own literal text supplies
// any "v" or path prefix, so a version carrying one of those already would
// double it up — that is refused here, not silently accepted.
var bareVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// newTagCmd is the release-tag parent: today, just "create", but grouped so
// a future companion verb (e.g. a local-only "tag delete") has somewhere to
// live without moving this one.
func newTagCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tag",
		Short: "Create and push a release tag",
	}
	cmd.AddCommand(newTagCreateCmd())
	return cmd
}

func newTagCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <version> --shape <pattern>",
		Short: "Create and push a signed release tag, refusing a version --shape rejects",
		Long: `create derives the tag it makes from --shape and <version>: --shape is the
exact one-line pattern "language-tools tag shape" prints for a module
("vX.Y.Z" for a root module, "<path>/vX.Y.Z" for a monorepo one), and
<version> is a bare "X.Y.Z" value — three dot-separated non-negative
integers, carrying no "v" prefix and no pre-release/build-metadata suffix of
its own, since --shape's literal text already supplies whatever prefix the
module requires.

Every check runs before create writes anything: <version> must fit the
placeholder shape, --shape must actually carry the "X.Y.Z" placeholder, and
the derived tag must not already exist locally. Any failure there is a
clean refusal — no tag is created, nothing is pushed.

On acceptance create makes a signed, annotated tag at the derived name and
pushes it through the same remote-advance path "git-tools push" uses on a
tag: never force, never overwriting an existing ref. The tag always signs
("git tag -s"), regardless of any ambient tag.forceSignAnnotated setting —
an explicit --annotate on the command line overrides that setting per
git-config(1), so create signs explicitly rather than relying on it.
Re-running create against a tag it already made is refused at the
local-existence check, not retried as a push — the clean retry for a push
that failed after the tag was already made is "git-tools push <tag>", not
create again.

Signing has two checks of its own, both before anything is pushed. Before
"git tag -s" runs, create proves git can actually produce a signature here —
a missing key or unreachable agent is refused by name rather than left to
surface as git tag -s's own opaque failure. After the tag is made, create
verifies it with "git tag -v": a tag whose signature does not verify is
deleted locally on the spot and never reaches the remote.

Like push, create always operates on the invoking process's own working
directory, under that repository's own policy file: --repo would move the
one, --config would swap the other, so both are refused.

Exit codes:
  0  success              the tag was created and pushed
  30 precondition_unmet   the content-guardrail scan found a marker, an
                           internal identifier, or a secret at the commit
                           being tagged, no key resolved to sign it, or the
                           tag it made failed its own signature verification
                           and was rolled back; no tag survives locally and
                           nothing was pushed
  40 not_found            the working directory is not a git working tree
  41 conflict             a local tag by that derived name already exists
  50 usage                <version> or --shape is missing or malformed, or
                           --repo/--config was passed
  60 transient            the remote rejected the push; re-run to retry
  90 internal             an underlying git command failed unexpectedly, or
                           an unverifiable tag's own rollback delete failed,
                           leaving it behind locally`,
		Args: cobra.ExactArgs(1),
		Example: strings.TrimLeft(`
  git-tools tag create 1.4.0 --shape vX.Y.Z
  git-tools tag create 0.4.0 --shape go/toolchain/vX.Y.Z
`, "\n"),
		RunE: func(cmd *cobra.Command, args []string) error {
			version := args[0]

			// --repo points every other verb at a different working
			// directory; --config swaps the policy file whose scan gates it.
			// create reuses push's own remote-advance path, which always
			// operates on the invoking process's own working directory, so it
			// refuses both exactly as push does rather than accepting a value
			// it would ignore.
			if cmd.Flags().Changed("repo") || cmd.Flags().Changed("config") {
				return finishUsage(cmd, nil, "usage.cli.tag_retargeting_flag",
					"tag create always operates on the invoking process's own working directory; --repo/--config are refused")
			}

			shape, _ := cmd.Flags().GetString("shape")
			if shape == "" {
				return finishUsage(cmd, nil, "usage.cli.missing_shape", "--shape is required")
			}

			tagName, mismatch := deriveTagName(version, shape)
			if mismatch != nil {
				return finishUsage(cmd, map[string]any{"version": version, "shape": shape},
					"usage.cli.tag_shape_mismatch", mismatch.Error())
			}

			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, nil, "internal.config.load_failed", "load configuration", err)
			}
			if err := openHere(cmd); err != nil {
				return err
			}

			ctx := cmd.Context()
			exists, err := gitexec.RefExists(ctx, ".", "tags", tagName)
			if err != nil {
				return finishErr(cmd, nil, "internal.git.show_ref_failed", fmt.Sprintf("check whether %s already exists", tagName), err)
			}
			if exists {
				return finishDiagnostic(cmd, map[string]any{"tag": tagName}, clikit.NewConflict, "conflict.git.tag_already_exists",
					fmt.Sprintf("tag %s already exists locally; create never overwrites or force-pushes an existing tag", tagName),
					clikit.Manual(fmt.Sprintf("choose a new version, or delete %s locally and on the remote first if it was made in error", tagName)),
					map[string]any{"tag": tagName})
			}

			// The content-guardrail scan is the last read-only precondition
			// before create writes anything: a tag can point at a commit that
			// never itself went through a scanned merge or push (tagging a
			// branch directly), so create carries its own gate rather than
			// depending on some earlier verb having already run one. A
			// warn-only caveat carries through into pushRef's own result
			// below rather than being dropped.
			gateCaveats, err := scanGate(cmd, cfg, ".", "tag", map[string]any{"tag": tagName})
			if err != nil {
				return err
			}

			// create signs unconditionally, so its precondition is checked
			// here rather than left to fall through to "git tag -s"'s own
			// failure below, which would otherwise surface as one generic
			// tag_create_failed no matter the actual cause.
			available, detail, err := signing.NewProber(&git.Repo{Dir: "."}).Available(ctx)
			if err != nil {
				return finishErr(cmd, nil, "internal.git.signing_probe_failed", "test whether git can sign the tag", err)
			}
			if !available {
				return finishDiagnostic(cmd, map[string]any{"tag": tagName}, clikit.NewPreconditionUnmet,
					"precondition_unmet.git.signing_key_unresolved",
					fmt.Sprintf("no key resolved for commit signing, so tag %s cannot be signed: %s", tagName, detail),
					clikit.Manual("configure a signing key (gpg.format plus user.signingkey, or this environment's signing setup) and re-run; nothing was tagged"),
					map[string]any{"tag": tagName})
			}

			// -s, not -a: an explicit --annotate on the command line overrides
			// tag.forceSignAnnotated (git-config(1)), so relying on ambient
			// config to sign a release tag silently produces an unsigned one.
			// -s implies -a and always signs, regardless of ambient config.
			res, err := gitexec.RunGit(ctx, ".", "tag", "-s", tagName, "-m", tagName)
			if err != nil {
				return finishErr(cmd, nil, "internal.git.tag_create_failed", fmt.Sprintf("create tag %s", tagName), err)
			}
			if res.ExitCode != 0 {
				return finishErr(cmd, nil, "internal.git.tag_create_failed", fmt.Sprintf("create tag %s", tagName),
					fmt.Errorf("%s", strings.TrimSpace(string(res.Stderr))))
			}

			// Verify the tag just made before anything is pushed: the
			// precondition check above proves git can produce a signature,
			// not that the signature this tag actually carries verifies (a
			// mismatched trust store can pass the former and fail the
			// latter). Running this ahead of the push below means a failure
			// here never has to touch the remote — only the local tag.
			verify, err := gitexec.RunGit(ctx, ".", "tag", "-v", tagName)
			if err != nil {
				return finishErr(cmd, nil, "internal.git.tag_verify_failed", fmt.Sprintf("verify tag %s", tagName), err)
			}
			if verify.ExitCode != 0 {
				verifyDetail := strings.TrimSpace(string(verify.Stderr))
				del, delErr := gitexec.RunGit(ctx, ".", "tag", "-d", tagName)
				if delErr != nil || del.ExitCode != 0 {
					// The one outcome here create can neither classify nor
					// unwind: its own rollback failed, so an unverifiable tag
					// is still sitting in the local repository and only a
					// person can clear it. That is what "internal" is for.
					var rollbackDetail string
					if delErr != nil {
						rollbackDetail = delErr.Error()
					} else {
						rollbackDetail = strings.TrimSpace(string(del.Stderr))
					}
					// Not finishErr: its fixed "retry" triage is wrong advice
					// here, since the surviving tag makes an identical re-run
					// fail the existence check instead. The manual delete has
					// to come first.
					return finishDiagnostic(cmd, map[string]any{"tag": tagName}, clikit.NewInternal,
						"internal.git.tag_rollback_failed",
						fmt.Sprintf("tag %s failed its own post-creation signature verification (%s), and the rollback delete also failed (%s)", tagName, verifyDetail, rollbackDetail),
						clikit.Manual(fmt.Sprintf("remove the tag by hand with `git tag -d %s` — it is still present locally and nothing was pushed — then fix this repository's signing trust configuration before re-running", tagName)),
						map[string]any{"tag": tagName})
				}
				// A verify failure the rollback did unwind is the same unmet
				// signing precondition the probe above reports, caught at the
				// only checkpoint that can see it: the probe proves git can
				// produce a signature, this proves the signature it produced
				// verifies. The rollback leaves the repository exactly as
				// create found it — no tag, nothing pushed — so this is a
				// precondition refusal, not an internal fault.
				return finishDiagnostic(cmd, map[string]any{"tag": tagName}, clikit.NewPreconditionUnmet,
					"precondition_unmet.git.tag_signature_unverified",
					fmt.Sprintf("tag %s failed its own post-creation signature verification and was rolled back: %s", tagName, verifyDetail),
					clikit.Manual("fix this repository's signing trust configuration (the signing key, and the allowed-signers file or keyring the verifier reads) so a tag it signs also verifies, then re-run; no tag survives locally and nothing was pushed"),
					map[string]any{"tag": tagName})
			}

			return pushRef(cmd, cfg, tagName, "tag", "refs/tags/"+tagName, gateCaveats)
		},
	}
	cmd.Flags().String("shape", "", `required: the tag pattern "language-tools tag shape" prints for this module ("vX.Y.Z", or "<path>/vX.Y.Z" for a monorepo module)`)
	return cmd
}

// deriveTagName builds the tag name create actually makes: shape with its
// "X.Y.Z" placeholder replaced by version. It never touches git — both
// checks here run before any git write, and either one failing means no tag
// is made and nothing is pushed.
func deriveTagName(version, shape string) (string, error) {
	idx := strings.Index(shape, versionPlaceholder)
	if idx == -1 {
		return "", fmt.Errorf("shape %q carries no %q version placeholder; it must be the exact pattern `language-tools tag shape` prints, and version %q has nowhere to go in it", shape, versionPlaceholder, version)
	}
	if !bareVersion.MatchString(version) {
		return "", fmt.Errorf("version %q is not three dot-separated non-negative integers, as shape %q's %q placeholder requires (no leading \"v\", no pre-release/build-metadata suffix — shape's own text supplies any prefix)", version, shape, versionPlaceholder)
	}
	return shape[:idx] + version + shape[idx+len(versionPlaceholder):], nil
}
