// End-to-end proof for resolveBetterleaksPath's sibling-file fallback (see
// scan.go): with GIT_TOOLS_BETTERLEAKS_BIN unset, a real betterleaks binary
// sitting next to the running git-tools executable is still found and used.
// Every case here copies the shared buildCLI(t) binary into its own private
// t.TempDir() before placing a file literally named betterleaks next to it:
// the shared cliBinDir must stay betterleaks-free, since
// secret_scan_categorized_severity_test.go's
// TestMandatoryCredentialScan_UnconfiguredRefusesEveryEntryPoint and
// TestMandatoryCredentialScan_NonexistentPathRefusesTheSameWay depend on that
// directory never resolving a working scanner.
package cli_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// copyCLIToPrivateDir copies the shared built git-tools binary into a fresh,
// private t.TempDir() and returns the copy's path, so a test can place a
// sibling file next to it without touching the shared cliBinDir every other
// test built from the same binary relies on staying betterleaks-free.
func copyCLIToPrivateDir(t *testing.T, sharedBin string) string {
	t.Helper()
	src, err := os.Open(sharedBin)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	dir := t.TempDir()
	dstPath := filepath.Join(dir, filepath.Base(sharedBin))
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	return dstPath
}

// TestSiblingBetterleaksFallback_ScanSecretsSucceedsWithoutTheEnvVar proves
// the whole chain end to end: GIT_TOOLS_BETTERLEAKS_BIN unset, a real
// betterleaks binary present next to the running executable, scan secrets
// succeeds anyway, through the sibling fallback alone.
func TestSiblingBetterleaksFallback_ScanSecretsSucceedsWithoutTheEnvVar(t *testing.T) {
	privateBin := copyCLIToPrivateDir(t, buildCLI(t))
	sibling := filepath.Join(filepath.Dir(privateBin), "betterleaks")
	if _, err := writeBetterleaksStub(filepath.Dir(privateBin), cleanBetterleaksReport); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(filepath.Dir(privateBin), "betterleaks-stub"), sibling); err != nil {
		t.Fatal(err)
	}

	dir := initRepo(t)
	t.Setenv(betterleaksBinEnvVar, "")

	r, exit := runCLIIn(t, privateBin, dir, "scan", "secrets")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0 (the sibling betterleaks must be found with no env var set): %+v", r.Status, exit, r)
	}
}

// TestSiblingBetterleaksFallback_MalformedSiblingRefusesAsUnconfigured proves
// every shape a betterleaks-named sibling can take that is not a runnable
// binary refuses the same way an absent one does: precondition_unmet, naming
// the scanner as unconfigured -- an actionable, expected gap -- never a
// status:"internal" fault from deep inside the exec githooks attempts. The
// first two shapes are rejected by resolveBetterleaksPath's own usability
// check; the last two carry an executable bit and pass every metadata check,
// so only the kernel's refusal to exec them reveals what they are (see
// scan.go's betterleaksStarts). Each case drives the built binary against a
// repo with a committed file, so the credential scan really does reach
// betterleaks rather than short-circuiting on an empty candidate list.
func TestSiblingBetterleaksFallback_MalformedSiblingRefusesAsUnconfigured(t *testing.T) {
	cases := []struct {
		name  string
		plant func(t *testing.T, sibling string)
	}{
		{"directory", func(t *testing.T, sibling string) {
			if err := os.Mkdir(sibling, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"non_executable_file", func(t *testing.T, sibling string) {
			if err := os.WriteFile(sibling, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"zero_byte_executable", func(t *testing.T, sibling string) {
			if err := os.WriteFile(sibling, nil, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"garbage_content_executable", func(t *testing.T, sibling string) {
			if err := os.WriteFile(sibling, []byte("\x00\x01not an executable format\x00"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			privateBin := copyCLIToPrivateDir(t, buildCLI(t))
			c.plant(t, filepath.Join(filepath.Dir(privateBin), "betterleaks"))

			dir := initRepo(t)
			t.Setenv(betterleaksBinEnvVar, "")

			r, exit := runCLIIn(t, privateBin, dir, "scan", "secrets")
			assertCredentialScannerUnconfigured(t, r, exit)
		})
	}
}

// TestSiblingBetterleaksFallback_NeitherSourceResolvesStillRefuses proves the
// fail-closed floor still holds: with the env var unset and no betterleaks
// file next to the running executable either, scan secrets still refuses
// with precondition_unmet, exactly as it did before this fallback existed.
func TestSiblingBetterleaksFallback_NeitherSourceResolvesStillRefuses(t *testing.T) {
	privateBin := copyCLIToPrivateDir(t, buildCLI(t))

	dir := initRepo(t)
	t.Setenv(betterleaksBinEnvVar, "")

	r, exit := runCLIIn(t, privateBin, dir, "scan", "secrets")
	assertCredentialScannerUnconfigured(t, r, exit)
}
