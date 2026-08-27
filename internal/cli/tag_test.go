// Tests for the tag create verb: shape/version validation before any git
// write, the accept path through push's own remote-advance mechanism
// (never a second push implementation), the existing-tag refusal, and the
// pinned edge cases in its acceptance spec. Every fixture here is a tmpdir
// repo with a local bare-or-nonexistent remote — no test touches a real
// remote or the network (PQ-BAR).
package cli_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// localTags lists dir's local tags, or "" if there are none — used to prove
// a refused create left no tag behind.
func localTags(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "tag", "-l")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git tag -l: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestTagCreate_RejectsBeforeAnyGitWrite(t *testing.T) {
	bin := buildCLI(t)

	cases := []struct {
		name        string
		args        []string
		wantMessage string
	}{
		{
			name:        "empty version",
			args:        []string{"tag", "create", "", "--shape", "vX.Y.Z"},
			wantMessage: `version ""`,
		},
		{
			name:        "version already carries a v prefix",
			args:        []string{"tag", "create", "v1.2.3", "--shape", "vX.Y.Z"},
			wantMessage: `version "v1.2.3"`,
		},
		{
			name:        "shape carries no version placeholder",
			args:        []string{"tag", "create", "1.2.3", "--shape", "release"},
			wantMessage: `shape "release" carries no`,
		},
		{
			name:        "missing --shape",
			args:        []string{"tag", "create", "1.2.3"},
			wantMessage: "--shape is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := initRepo(t)
			newBareRemote(t, dir)
			r, exit := runCLIIn(t, bin, dir, tc.args...)
			if r.Status != "usage" || exit != 50 {
				t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
			}
			if len(r.Errors) == 0 {
				t.Fatalf("no governing error recorded: %+v", r)
			}
			message, _ := r.Errors[0]["message"].(string)
			if !strings.Contains(message, tc.wantMessage) {
				t.Fatalf("governing error message %q does not contain %q", message, tc.wantMessage)
			}
			if tags := localTags(t, dir); tags != "" {
				t.Fatalf("rejected create left a local tag behind: %q", tags)
			}
		})
	}
}

func TestTagCreate_RetargetingFlagsRefused(t *testing.T) {
	bin := buildCLI(t)

	for _, args := range [][]string{
		{"tag", "create", "1.2.3", "--shape", "vX.Y.Z", "--repo", "."},
		{"tag", "create", "1.2.3", "--shape", "vX.Y.Z", "--config", ".git-tools.yaml"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			dir := initRepo(t)
			newBareRemote(t, dir)
			r, exit := runCLIIn(t, bin, dir, args...)
			if r.Status != "usage" || exit != 50 {
				t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
			}
			message, _ := r.Errors[0]["message"].(string)
			if !strings.Contains(message, "--repo/--config are refused") {
				t.Fatalf("governing error message %q does not name the refused flags", message)
			}
			if tags := localTags(t, dir); tags != "" {
				t.Fatalf("rejected create left a local tag behind: %q", tags)
			}
		})
	}
}

func TestTagCreate_BareShape_CreatesAndPushes(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	bare := newBareRemote(t, dir)

	r, exit := runCLIIn(t, bin, dir, "tag", "create", "1.4.0", "--shape", "vX.Y.Z")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if ref, _ := r.Data["ref"].(string); ref != "v1.4.0" {
		t.Fatalf("data.ref = %v, want v1.4.0", r.Data["ref"])
	}

	localID := runGit(t, dir, "rev-parse", "v1.4.0")
	remoteID := runGit(t, bare, "rev-parse", "refs/tags/v1.4.0")
	if localID != remoteID {
		t.Fatalf("local tag %s != remote tag %s", localID, remoteID)
	}
}

// TestTagCreate_SignsRegardlessOfForceSignAnnotated proves create's tag
// always signs, even when tag.forceSignAnnotated is unset. An explicit
// --annotate on the git command line overrides that config (git-config(1)),
// so a create that passed "-a" instead of "-s" would silently produce an
// unsigned tag on a host that relies on the config to sign for it — exactly
// the defect this test guards against.
func TestTagCreate_SignsRegardlessOfForceSignAnnotated(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	newBareRemote(t, dir)
	// signingRepo sets commit.gpgsign but never touches the tag namespace —
	// tag.forceSignAnnotated is absent here, exactly the state that must not
	// matter to create's own signing.

	r, exit := runCLIIn(t, bin, dir, "tag", "create", "1.4.0", "--shape", "vX.Y.Z")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}

	out, err := exec.Command("git", "-C", dir, "tag", "-v", "v1.4.0").CombinedOutput()
	if err != nil {
		t.Fatalf("git tag -v v1.4.0: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Good \"git\" signature") {
		t.Fatalf("tag v1.4.0 did not carry a verifying signature:\n%s", out)
	}
}

func TestTagCreate_PrefixedShape_CreatesAndPushes(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	bare := newBareRemote(t, dir)

	r, exit := runCLIIn(t, bin, dir, "tag", "create", "0.4.0", "--shape", "go/toolchain/vX.Y.Z")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if ref, _ := r.Data["ref"].(string); ref != "go/toolchain/v0.4.0" {
		t.Fatalf("data.ref = %v, want go/toolchain/v0.4.0", r.Data["ref"])
	}

	localID := runGit(t, dir, "rev-parse", "go/toolchain/v0.4.0")
	remoteID := runGit(t, bare, "rev-parse", "refs/tags/go/toolchain/v0.4.0")
	if localID != remoteID {
		t.Fatalf("local tag %s != remote tag %s", localID, remoteID)
	}
}

// TestTagCreate_PinnedShapeForms_MatchLanguageToolsContract pins the exact
// two pattern strings "language-tools tag shape" prints (CONTRACT-LT-CONFIG,
// D8/OQ7): "vX.Y.Z" for a root module, "<path>/vX.Y.Z" for a monorepo one.
// git-tools and language-tools each pin these literals independently in
// their own test suite, so a change to either side's placeholder grammar
// shows up here without the two repos sharing code.
func TestTagCreate_PinnedShapeForms_MatchLanguageToolsContract(t *testing.T) {
	bin := buildCLI(t)

	cases := []struct {
		shape   string
		version string
		wantTag string
	}{
		{shape: "vX.Y.Z", version: "2.0.0", wantTag: "v2.0.0"},
		{shape: "go/toolchain/vX.Y.Z", version: "1.1.1", wantTag: "go/toolchain/v1.1.1"},
	}
	for _, tc := range cases {
		t.Run(tc.shape, func(t *testing.T) {
			dir := initRepo(t)
			newBareRemote(t, dir)
			r, exit := runCLIIn(t, bin, dir, "tag", "create", tc.version, "--shape", tc.shape)
			if r.Status != "success" || exit != 0 {
				t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
			}
			if ref, _ := r.Data["ref"].(string); ref != tc.wantTag {
				t.Fatalf("data.ref = %v, want %s", r.Data["ref"], tc.wantTag)
			}
		})
	}
}

func TestTagCreate_ExistingTag_RefusedCleanly(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	bare := newBareRemote(t, dir)

	if r, exit := runCLIIn(t, bin, dir, "tag", "create", "1.0.0", "--shape", "vX.Y.Z"); r.Status != "success" || exit != 0 {
		t.Fatalf("first create: status=%s exit=%d: %+v", r.Status, exit, r)
	}
	remoteBefore := runGit(t, bare, "rev-parse", "refs/tags/v1.0.0")

	r, exit := runCLIIn(t, bin, dir, "tag", "create", "1.0.0", "--shape", "vX.Y.Z")
	if r.Status != "conflict" || exit != 41 {
		t.Fatalf("re-run: status=%s exit=%d, want conflict/41: %+v", r.Status, exit, r)
	}
	message, _ := r.Errors[0]["message"].(string)
	if !strings.Contains(message, "v1.0.0 already exists locally") {
		t.Fatalf("governing error message %q does not name the existing tag", message)
	}

	remoteAfter := runGit(t, bare, "rev-parse", "refs/tags/v1.0.0")
	if remoteAfter != remoteBefore {
		t.Fatalf("remote tag moved on a refused re-run: %s -> %s (never overwrite, never force)", remoteBefore, remoteAfter)
	}
}

// TestTagCreate_DetachedHEAD_Succeeds pins that create, like push, treats a
// tag push as exempt from any current-branch requirement: a tag names a
// commit, not a branch, and detached HEAD is a legitimate place to cut one
// from.
func TestTagCreate_DetachedHEAD_Succeeds(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	bare := newBareRemote(t, dir)
	tip := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "checkout", "-q", "--detach", tip)

	r, exit := runCLIIn(t, bin, dir, "tag", "create", "1.0.0", "--shape", "vX.Y.Z")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if got := runGit(t, bare, "rev-parse", "refs/tags/v1.0.0^{commit}"); got != tip {
		t.Fatalf("pushed tag resolves to %s, want detached HEAD's commit %s", got, tip)
	}
}

// TestTagCreate_NoOriginRemote_FailsCleanly and
// TestTagCreate_UnreachableRemote_FailsCleanly pin the two ways the shared
// push path can fail after the tag itself was made: git's own failure to
// query a remote is surfaced as an internal error, not silently swallowed
// or misreported as a retryable rejection. Neither test touches a real
// remote: one has no remote configured at all, the other points "origin" at
// a local path nothing has ever created.
func TestTagCreate_NoOriginRemote_FailsCleanly(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t) // newBareRemote is never called: no "origin" exists.

	r, exit := runCLIIn(t, bin, dir, "tag", "create", "1.0.0", "--shape", "vX.Y.Z")
	if r.Status != "internal" || exit != 90 {
		t.Fatalf("status=%s exit=%d, want internal/90: %+v", r.Status, exit, r)
	}
	if tags := localTags(t, dir); tags != "v1.0.0" {
		t.Fatalf("local tags = %q, want the tag create still made before the push it could not attempt", tags)
	}
}

func TestTagCreate_UnreachableRemote_FailsCleanly(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	unreachable := filepath.Join(t.TempDir(), "never-created.git")
	runGit(t, dir, "remote", "add", "origin", unreachable)

	r, exit := runCLIIn(t, bin, dir, "tag", "create", "1.0.0", "--shape", "vX.Y.Z")
	if r.Status != "internal" || exit != 90 {
		t.Fatalf("status=%s exit=%d, want internal/90: %+v", r.Status, exit, r)
	}
	if tags := localTags(t, dir); tags != "v1.0.0" {
		t.Fatalf("local tags = %q, want the tag create still made before the push it could not attempt", tags)
	}
}

// TestTagCreate_ExitCodeTable pins the full 0/40/41/50/90 contract create's
// own --help documents against one representative case per code (60,
// transient, is pinned separately in push's own diverged-history test: the
// underlying rejection path is identical, and reproducing a genuine
// non-fast-forward here would just re-test push's own mechanism).
func TestTagCreate_ExitCodeTable(t *testing.T) {
	bin := buildCLI(t)

	cases := []struct {
		name     string
		setup    func(t *testing.T) string // returns the repo dir
		args     []string
		wantExit int
	}{
		{
			name: "success",
			setup: func(t *testing.T) string {
				dir := initRepo(t)
				newBareRemote(t, dir)
				return dir
			},
			args:     []string{"tag", "create", "1.0.0", "--shape", "vX.Y.Z"},
			wantExit: 0,
		},
		{
			name: "not_found: not a git working tree",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			args:     []string{"tag", "create", "1.0.0", "--shape", "vX.Y.Z"},
			wantExit: 40,
		},
		{
			name: "conflict: tag already exists",
			setup: func(t *testing.T) string {
				dir := initRepo(t)
				newBareRemote(t, dir)
				runGit(t, dir, "tag", "-a", "v1.0.0", "-m", "v1.0.0")
				return dir
			},
			args:     []string{"tag", "create", "1.0.0", "--shape", "vX.Y.Z"},
			wantExit: 41,
		},
		{
			name: "usage: shape rejects version",
			setup: func(t *testing.T) string {
				dir := initRepo(t)
				newBareRemote(t, dir)
				return dir
			},
			args:     []string{"tag", "create", "v1.0.0", "--shape", "vX.Y.Z"},
			wantExit: 50,
		},
		{
			name: "internal: no remote to push to",
			setup: func(t *testing.T) string {
				return initRepo(t)
			},
			args:     []string{"tag", "create", "1.0.0", "--shape", "vX.Y.Z"},
			wantExit: 90,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.setup(t)
			_, exit := runCLIIn(t, bin, dir, tc.args...)
			if exit != tc.wantExit {
				t.Fatalf("exit=%d, want %d", exit, tc.wantExit)
			}
		})
	}
}

func TestTagCreate_HelpIsComplete(t *testing.T) {
	bin := buildCLI(t)

	out, err := exec.Command(bin, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("--help exited non-zero: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "tag") {
		t.Fatalf("top-level --help does not mention tag:\n%s", out)
	}

	out, err = exec.Command(bin, "tag", "create", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("tag create --help exited non-zero: %v\n%s", err, out)
	}
	text := string(out)
	for _, want := range []string{"Usage:", "Examples:", "--shape", "Exit codes:", "never overwriting an existing ref"} {
		if !strings.Contains(text, want) {
			t.Errorf("tag create --help missing %q:\n%s", want, text)
		}
	}
}
