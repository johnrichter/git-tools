package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/git-tools/internal/gitexec"
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

Like push, create always operates on the invoking process's own working
directory: --repo/--config would retarget it, so both are refused.

Exit codes:
  0  success              the tag was created and pushed
  40 not_found            the working directory is not a git working tree
  41 conflict             a local tag by that derived name already exists
  50 usage                <version> or --shape is missing or malformed, or
                           --repo/--config was passed
  60 transient            the remote rejected the push; re-run to retry
  90 internal             an underlying git command failed unexpectedly`,
		Args: cobra.ExactArgs(1),
		Example: strings.TrimLeft(`
  git-tools tag create 1.4.0 --shape vX.Y.Z
  git-tools tag create 0.4.0 --shape go/toolchain/vX.Y.Z
`, "\n"),
		RunE: func(cmd *cobra.Command, args []string) error {
			version := args[0]

			// --repo/--config retarget every other verb at a different
			// working directory or settings file. create reuses push's own
			// remote-advance path, which always operates on the invoking
			// process's own working directory, so it refuses both exactly
			// as push does rather than accepting a value it would ignore.
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

			return pushRef(cmd, cfg, tagName, "tag", "refs/tags/"+tagName)
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
