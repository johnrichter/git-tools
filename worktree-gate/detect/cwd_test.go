package detect

import (
	"encoding/json"
	"os"
	"testing"
)

// cwdCorpusCase mirrors one entry of testdata/cwd-corpus.json: a Bash
// command and the statically-resolved effective working directory of its
// first git invocation, per SC-CWD-RESOLVER-CONTRACT. "" means the session
// cwd governs unchanged (no preceding cd/-C); "DENY" means the target is
// unresolvable from the command text alone.
type cwdCorpusCase struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Expect  string `json:"expect"`
}

type cwdCorpus struct {
	Cases       []cwdCorpusCase `json:"cases"`
	Description string          `json:"description"`
}

func loadCwdCorpus(t *testing.T) cwdCorpus {
	t.Helper()
	b, err := os.ReadFile("testdata/cwd-corpus.json")
	if err != nil {
		t.Fatalf("testdata/cwd-corpus.json: %v", err)
	}
	var c cwdCorpus
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("testdata/cwd-corpus.json: %v", err)
	}
	if len(c.Cases) == 0 {
		t.Fatal("testdata/cwd-corpus.json carries no cases")
	}
	return c
}

// TestResolveEffectiveCWD_MatchesGoldenCorpus drives resolveEffectiveCWD --
// the same static resolver decideBash calls through effectiveBashCWD --
// against the golden corpus the shell gate's own resolver (git-gate.sh
// --print-eff-dir) is pinned to. One shared corpus, mirrored byte-identical
// into this package, is what keeps the two resolvers from silently
// diverging (SC-CWD-RESOLVER-CONTRACT).
func TestResolveEffectiveCWD_MatchesGoldenCorpus(t *testing.T) {
	corpus := loadCwdCorpus(t)
	for _, c := range corpus.Cases {
		t.Run(c.Name, func(t *testing.T) {
			dir, unresolvable := resolveEffectiveCWD(c.Command, "")
			if c.Expect == "DENY" {
				if !unresolvable {
					t.Errorf("resolveEffectiveCWD(%q) = (%q, unresolvable=%v), want unresolvable", c.Command, dir, unresolvable)
				}
				return
			}
			if unresolvable || dir != c.Expect {
				t.Errorf("resolveEffectiveCWD(%q) = (%q, unresolvable=%v), want (%q, unresolvable=false)", c.Command, dir, unresolvable, c.Expect)
			}
		})
	}
}

// testProvisionedPath is the corpus topology's provisioned path, the same
// value the decide-bash-corpus.json cases use for the CLI's absolute
// location.
const testProvisionedPath = "/plugin-data/bin/git-tools"

// TestResolveEffectiveCWD_ProvisionedCLI drives D-4/R7 directly: the
// provisioned CLI, invoked by its exact provisioned path, composes its `-C`
// target (split and glued) onto the effective directory exactly as git's own
// `-C` composes. A bare name, a relative path, and an arbitrary absolute
// path each stay unrecognized as a head word, so none of them composes
// anything -- the pinned identity negatives this shares with sc15Identity.
func TestResolveEffectiveCWD_ProvisionedCLI(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"dash-C-split", testProvisionedPath + " worktree remove -C /repo/wt", "/repo/wt"},
		{"dash-C-glued", testProvisionedPath + " worktree remove -C/repo/wt", "/repo/wt"},
		{"bare-word-composes-nothing", "git-tools worktree remove -C /repo/wt", ""},
		{"relative-path-composes-nothing", "./git-tools worktree remove -C /repo/wt", ""},
		{"arbitrary-absolute-path-composes-nothing", "/usr/local/bin/git-tools worktree remove -C /repo/wt", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, unresolvable := resolveEffectiveCWD(c.command, testProvisionedPath)
			if unresolvable || dir != c.want {
				t.Errorf("resolveEffectiveCWD(%q, %q) = (%q, unresolvable=%v), want (%q, unresolvable=false)", c.command, testProvisionedPath, dir, unresolvable, c.want)
			}
		})
	}
}

// TestResolveEffectiveCWD_ProvisionedCLI_KeyedOnHeadWordNotFlag pins R7's own
// caution: a `-C` composes only behind the provisioned CLI's head word,
// never behind an unrelated command that happens to carry the same flag
// spelling (grep's context-lines `-C`, here).
func TestResolveEffectiveCWD_ProvisionedCLI_KeyedOnHeadWordNotFlag(t *testing.T) {
	dir, unresolvable := resolveEffectiveCWD("grep -rn -C 3 pat /repo/wt", testProvisionedPath)
	if unresolvable || dir != "" {
		t.Errorf(`resolveEffectiveCWD("grep -rn -C 3 pat /repo/wt", ...) = (%q, unresolvable=%v), want ("", unresolvable=false): -C must not compose off grep`, dir, unresolvable)
	}
}

// TestResolveEffectiveCWD_ProvisionedCLI_EmptyParamDisablesRecognition pins
// that an empty provisionedPath (the argv parameter absent) disables the
// recognition outright, never falling back to a bare-name or basename match.
func TestResolveEffectiveCWD_ProvisionedCLI_EmptyParamDisablesRecognition(t *testing.T) {
	dir, unresolvable := resolveEffectiveCWD(testProvisionedPath+" worktree remove -C /repo/wt", "")
	if unresolvable || dir != "" {
		t.Errorf("resolveEffectiveCWD(...) with no provisioned path = (%q, unresolvable=%v), want (\"\", unresolvable=false)", dir, unresolvable)
	}
}

// TestResolveEffectiveCWD_ProvisionedCLI_Adversarial adds SDET coverage
// beyond the pinned corpus: flag-before-verb ordering, a repeated `-C`
// (later wins, mirroring applyGitOptions), a quoted target, a relative
// target composed onto a preceding `cd`, and confirmation that `--repo` and
// `--repo=` (git-tools' other two spellings of this same flag, per the
// doc-committed scope narrowing in applyGitToolsOptions) compose NOTHING --
// pinning the deliberate deviation from the flag's literal three-spelling
// text so a future change to that boundary shows up here as a test diff,
// not a silent behavior change.
func TestResolveEffectiveCWD_ProvisionedCLI_Adversarial(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"flag-before-verb", testProvisionedPath + " -C /repo/wt worktree remove", "/repo/wt"},
		{"repeated-dashC-later-wins", testProvisionedPath + " worktree remove -C /repo/first -C /repo/second", "/repo/second"},
		{"quoted-target", testProvisionedPath + ` worktree remove -C "/repo/wt"`, "/repo/wt"},
		{"relative-target-composes-onto-cd", "cd /repo && " + testProvisionedPath + " worktree remove -C wt", "/repo/wt"},
		{"repo-spaced-composes-nothing", testProvisionedPath + " worktree remove --repo /repo/wt", ""},
		{"repo-glued-composes-nothing", testProvisionedPath + " worktree remove --repo=/repo/wt", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, unresolvable := resolveEffectiveCWD(c.command, testProvisionedPath)
			if unresolvable || dir != c.want {
				t.Errorf("resolveEffectiveCWD(%q, %q) = (%q, unresolvable=%v), want (%q, unresolvable=false)", c.command, testProvisionedPath, dir, unresolvable, c.want)
			}
		})
	}
}

// TestComposeInteriorCWD_ProvisionedCLI pins that composeInteriorCWD threads
// provisionedPath through to the interior's own resolveEffectiveCWD call, so
// a provisioned-CLI `-C` inside a `$(...)` composes the same way it does at
// the top level.
func TestComposeInteriorCWD_ProvisionedCLI(t *testing.T) {
	interior := testProvisionedPath + " worktree remove -C /repo/wt"
	dir, unresolvable := composeInteriorCWD("/somewhere-else", false, interior, testProvisionedPath)
	if unresolvable || dir != "/repo/wt" {
		t.Errorf("composeInteriorCWD(..., %q, %q) = (%q, %v), want (/repo/wt, false)", interior, testProvisionedPath, dir, unresolvable)
	}
}

// TestEffectiveBashCWD_ProvisionedCLI pins that effectiveBashCWD (decideBash's
// own entry point) threads provisionedPath through to resolveEffectiveCWD, so
// the provisioned CLI's `-C` composes the session's effective cwd, not just
// the lower-level resolver's return value.
func TestEffectiveBashCWD_ProvisionedCLI(t *testing.T) {
	dir, unresolvable := effectiveBashCWD("/repo", testProvisionedPath+" worktree remove -C /repo/wt", testProvisionedPath)
	if unresolvable || dir != "/repo/wt" {
		t.Errorf("effectiveBashCWD(...) = (%q, %v), want (/repo/wt, false)", dir, unresolvable)
	}
}
