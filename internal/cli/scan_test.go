package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/githooks"
)

// TestGitToolsSkipRules_WorktreePatternIsRootAnchored proves gitToolsSkipRules'
// own .claude/worktrees/** rule matches only that literal prefix at the
// scanned root, never a worktrees/ directory nested elsewhere in the tree
// (which may be legitimately tracked content unrelated to this fleet's
// nested-worktree convention) and never a .claude/worktrees/ that itself sits
// under some other directory instead of at the root.
func TestGitToolsSkipRules_WorktreePatternIsRootAnchored(t *testing.T) {
	rule := gitToolsSkipRules[len(gitToolsSkipRules)-1]
	if rule.Pattern != ".claude/worktrees/**" || rule.Class != githooks.SkipClass {
		t.Fatalf("gitToolsSkipRules' last rule = %+v, want the .claude/worktrees/** skip rule appended after githooks.DefaultSkipRules", rule)
	}

	cases := []struct {
		path string
		want bool
	}{
		{".claude/worktrees/native/foo.go", true},
		{".claude/worktrees/native/nested/deep/bar.env", true},
		{"plugins/foo/worktrees/bar.md", false},
		{"nested/.claude/worktrees/native/foo.go", false},
	}
	for _, c := range cases {
		matched, err := doublestar.Match(rule.Pattern, c.path)
		if err != nil {
			t.Fatalf("doublestar.Match(%q, %q): %v", rule.Pattern, c.path, err)
		}
		if matched != c.want {
			t.Errorf("doublestar.Match(%q, %q) = %v, want %v", rule.Pattern, c.path, matched, c.want)
		}
	}
}

// TestGitToolsSkipRules_IsDefensiveCopyOfDefaultSkipRules proves
// gitToolsSkipRules never shares backing storage with
// githooks.DefaultSkipRules: appending its own rule onto a copy, not onto
// DefaultSkipRules directly, is what keeps a future append to
// DefaultSkipRules (or to gitToolsSkipRules itself) from silently mutating
// the other.
func TestGitToolsSkipRules_IsDefensiveCopyOfDefaultSkipRules(t *testing.T) {
	if len(githooks.DefaultSkipRules) == 0 {
		t.Fatal("githooks.DefaultSkipRules is empty; nothing to compare against")
	}
	if len(gitToolsSkipRules) <= len(githooks.DefaultSkipRules) {
		t.Fatalf("gitToolsSkipRules has %d rules, want more than DefaultSkipRules' %d (its own .claude/worktrees/** rule appended)", len(gitToolsSkipRules), len(githooks.DefaultSkipRules))
	}
	if unsafe.SliceData(githooks.DefaultSkipRules) == unsafe.SliceData(gitToolsSkipRules) {
		t.Fatal("gitToolsSkipRules shares its backing array with githooks.DefaultSkipRules — mutating one through its shared storage could mutate the other")
	}
}

// TestCodeMarkerExemptRules_MatchesEveryCodeSuffixAnywhereInTree proves each
// codeMarkerExemptRules pattern matches its own suffix at any depth,
// including at the scanned root (unlike gitToolsSkipRules' root-anchored
// worktree pattern, this exemption must fire wherever a source file sits),
// and does not match an unrelated suffix or a suffix that merely appears
// mid-name rather than as the file's own extension.
func TestCodeMarkerExemptRules_MatchesEveryCodeSuffixAnywhereInTree(t *testing.T) {
	if len(codeMarkerExemptRules) != len(codeMarkerExemptSuffixes) {
		t.Fatalf("codeMarkerExemptRules has %d rules, want one per suffix (%d)", len(codeMarkerExemptRules), len(codeMarkerExemptSuffixes))
	}
	for i, suffix := range codeMarkerExemptSuffixes {
		rule := codeMarkerExemptRules[i]
		if rule.Class != githooks.SkipClass {
			t.Fatalf("codeMarkerExemptRules[%d].Class = %q, want githooks.SkipClass", i, rule.Class)
		}
		cases := []struct {
			path string
			want bool
		}{
			{"check_privacy" + suffix, true},
			{"scripts/check_privacy" + suffix, true},
			{"a/b/c" + suffix, true},
			{"check_privacy" + suffix + ".bak", false},
			{"check_privacy.md", false},
		}
		for _, c := range cases {
			matched, err := doublestar.Match(rule.Pattern, c.path)
			if err != nil {
				t.Fatalf("doublestar.Match(%q, %q): %v", rule.Pattern, c.path, err)
			}
			if matched != c.want {
				t.Errorf("suffix %s: doublestar.Match(%q, %q) = %v, want %v", suffix, rule.Pattern, c.path, matched, c.want)
			}
		}
	}
}

// writeExecutableStub writes an empty, executable file at dir/name and
// returns its path — enough for resolveBetterleaksPath's existence check to
// resolve it, with no real betterleaks behavior needed by these tests.
func writeExecutableStub(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestResolveBetterleaksPath_UnsetIsMandatoryFailure proves the credential
// scan's env-var opt-out is gone: an unset betterleaksBinEnvVar is now a real
// error (errBetterleaksUnconfigured), not a silent "" skip.
func TestResolveBetterleaksPath_UnsetIsMandatoryFailure(t *testing.T) {
	t.Setenv(betterleaksBinEnvVar, "")
	path, err := resolveBetterleaksPath()
	if path != "" {
		t.Fatalf("resolveBetterleaksPath() path = %q, want \"\" on failure", path)
	}
	if !errors.Is(err, errBetterleaksUnconfigured) {
		t.Fatalf("resolveBetterleaksPath() err = %v, want errBetterleaksUnconfigured", err)
	}
}

// TestResolveBetterleaksPath_NonexistentPathIsMandatoryFailure proves a
// configured-but-nonexistent path fails the same way as unset: it must not
// be handed to githooks.ScanCredentials as though it were real.
func TestResolveBetterleaksPath_NonexistentPathIsMandatoryFailure(t *testing.T) {
	t.Setenv(betterleaksBinEnvVar, filepath.Join(t.TempDir(), "does-not-exist"))
	path, err := resolveBetterleaksPath()
	if path != "" {
		t.Fatalf("resolveBetterleaksPath() path = %q, want \"\" on failure", path)
	}
	if !errors.Is(err, errBetterleaksUnconfigured) {
		t.Fatalf("resolveBetterleaksPath() err = %v, want errBetterleaksUnconfigured", err)
	}
}

// TestResolveBetterleaksPath_ExistingPathResolves proves the happy path: a
// path naming a real, existing file passes straight through with no error.
func TestResolveBetterleaksPath_ExistingPathResolves(t *testing.T) {
	bin := writeExecutableStub(t, t.TempDir(), "betterleaks")
	t.Setenv(betterleaksBinEnvVar, bin)
	path, err := resolveBetterleaksPath()
	if err != nil {
		t.Fatalf("resolveBetterleaksPath() err = %v, want nil", err)
	}
	if path != bin {
		t.Fatalf("resolveBetterleaksPath() = %q, want the configured path unchanged (%q)", path, bin)
	}
}

// TestScanCredentials_UnresolvedBinaryFailsWithoutInvokingBetterleaks proves
// scanCredentials never shells out at all when the binary path is
// unresolved: it returns errBetterleaksUnconfigured rather than attempting to
// run anything (which would otherwise fail loudly against a nonexistent
// binary) or silently reporting a clean scan.
func TestScanCredentials_UnresolvedBinaryFailsWithoutInvokingBetterleaks(t *testing.T) {
	t.Setenv(betterleaksBinEnvVar, "")
	findings, err := scanCredentials(t.TempDir(), &Config{})
	if !errors.Is(err, errBetterleaksUnconfigured) {
		t.Fatalf("scanCredentials err = %v, want errBetterleaksUnconfigured", err)
	}
	if findings != nil {
		t.Fatalf("scanCredentials returned %+v, want nil findings alongside the error", findings)
	}
}

// TestBetterleaksExtraRules_ConvertsIDAndRegexOnly proves the config-to-
// githooks conversion carries ID and Regex through and drops Category:
// githooks.BetterleaksRule has no Category field, so every betterleaks
// finding still reports Category "credentials" regardless of what an extra
// rule's own Category names (see Config.SecretScanExtraRules).
func TestBetterleaksExtraRules_ConvertsIDAndRegexOnly(t *testing.T) {
	in := []SecretScanExtraRule{
		{ID: "fixture-marker", Regex: "fixture-[0-9]+", Category: "financial"},
	}
	out := betterleaksExtraRules(in)
	if len(out) != 1 || out[0].ID != "fixture-marker" || out[0].Regex != "fixture-[0-9]+" {
		t.Fatalf("betterleaksExtraRules(%+v) = %+v, want ID/Regex carried through unchanged", in, out)
	}
}

// TestBetterleaksExtraAllowlist_ConvertsAllThreeFields proves the
// config-to-githooks allowlist conversion carries RuleID, Value, and Regex
// through unchanged.
func TestBetterleaksExtraAllowlist_ConvertsAllThreeFields(t *testing.T) {
	in := []SecretScanExtraAllowlistEntry{
		{RuleID: "fixture-marker", Value: "fixture-value", Regex: "fixture-.*"},
	}
	out := betterleaksExtraAllowlist(in)
	if len(out) != 1 || out[0].RuleID != "fixture-marker" || out[0].Value != "fixture-value" || out[0].Regex != "fixture-.*" {
		t.Fatalf("betterleaksExtraAllowlist(%+v) = %+v, want RuleID/Value/Regex carried through unchanged", in, out)
	}
}

// TestAddCategoryCounts_GroupsFindingsByCategory proves the three
// category-grouped output keys count only their own Category's findings,
// an empty Category (every scanner outside the credentials/pii/financial
// taxonomy) counts toward none of them, and an unrecognized Category is
// likewise ignored rather than crashing.
func TestAddCategoryCounts_GroupsFindingsByCategory(t *testing.T) {
	secrets := []githooks.Finding{
		{Path: "a", Category: "credentials"},
		{Path: "b", Category: "credentials"},
		{Path: "c", Category: "pii"},
		{Path: "d", Category: "financial"},
		{Path: "e", Category: ""},
		{Path: "f", Category: "unrecognized"},
	}
	result := &clikit.Result{}
	addCategoryCounts(result, secrets)
	want := map[string]int{"credentials_found": 2, "pii_found": 1, "financial_found": 1}
	for key, count := range want {
		got, _ := result.Data[key].(int)
		if got != count {
			t.Errorf("result.Data[%q] = %v, want %d", key, result.Data[key], count)
		}
	}
}

// TestAddCategoryCounts_ZeroFindingsStillSetsAllThreeKeys proves the three
// keys are always present at zero, never simply absent, so a consumer never
// has to distinguish "no credentials findings" from "this key was never
// computed".
func TestAddCategoryCounts_ZeroFindingsStillSetsAllThreeKeys(t *testing.T) {
	result := &clikit.Result{}
	addCategoryCounts(result, nil)
	for _, key := range []string{"credentials_found", "pii_found", "financial_found"} {
		got, ok := result.Data[key].(int)
		if !ok || got != 0 {
			t.Errorf("result.Data[%q] = %v (ok=%v), want 0", key, result.Data[key], ok)
		}
	}
}
