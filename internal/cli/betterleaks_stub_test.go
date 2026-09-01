// Shared betterleaks stub-binary plumbing for every cli_test package test
// that drives the built CLI as a subprocess: since the credential scan is
// now mandatory (see scan.go's errBetterleaksUnconfigured), a test whose
// point is something other than that mandatory-scan refusal itself needs a
// real, resolvable betterleaks binary in place before it can exercise
// scan/merge/push/rebase/tag create at all. TestMain (integration_test.go)
// points betterleaksBinEnvVar at the clean stub this file builds for the
// whole test binary run, so any test not otherwise concerned with
// betterleaks findings gets a working, zero-findings scan by default; a
// test that does care about a specific finding overrides it for its own
// scope with t.Setenv, exactly as writeFixtureBetterleaksBinary (used by
// scan_gate_test.go) and writeContentAwareBetterleaksBinary (used by
// merge_scan_test.go) already do.
package cli_test

import (
	"os"
	"path/filepath"
)

// betterleaksBinEnvVar is GIT_TOOLS_BETTERLEAKS_BIN, duplicated here as a
// literal rather than imported: this file's own package is cli_test, an
// external test package that cannot see the cli package's unexported
// betterleaksBinEnvVar constant.
const betterleaksBinEnvVar = "GIT_TOOLS_BETTERLEAKS_BIN"

// cleanBetterleaksReport is the betterleaks JSON report shape (an empty
// finding array) TestMain's default stub always prints: a real, working
// scanner that finds nothing, standing in for the fleet's actual provisioned
// betterleaks binary on a repository with nothing to flag.
const cleanBetterleaksReport = "[]\n"

// writeBetterleaksStub writes an executable POSIX shell script standing in
// for the betterleaks binary at dir/betterleaks-stub, printing report on
// stdout and exiting 1 -- the exit code real betterleaks uses when it has
// findings, which githooks.ScanCredentials ignores in favor of whether
// stdout parses as JSON. It never touches t.TempDir() itself, so it can run
// from TestMain (which has no *testing.T) as well as from a per-test helper.
func writeBetterleaksStub(dir, report string) (string, error) {
	bin := filepath.Join(dir, "betterleaks-stub")
	script := "#!/bin/sh\ncat <<'STUB_REPORT'\n" + report + "STUB_REPORT\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		return "", err
	}
	return bin, nil
}
