// End-to-end proof for resolveBetterleaksPath's sibling-file fallback (see
// scan.go): with GIT_TOOLS_BETTERLEAKS_BIN unset, a real betterleaks binary
// sitting next to the running git-tools executable is still found and used.
// Both cases here copy the shared buildCLI(t) binary into their own private
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
