// Pure unit tests for resolveBetterleaksPath's sibling-file fallback: the
// automatic discovery path that lets a running git-tools process find a
// betterleaks binary next to its own executable, with no environment
// variable needed. These use siblingBetterleaksPathFrom's injected
// self-lookup seam rather than a real compiled binary; see
// secret_scan_categorized_severity_test.go for the end-to-end proof against
// the actual built CLI.
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestResolveBetterleaksPath_EnvVarWinsOverSibling proves the env var stays
// an explicit override: when both it and a sibling file resolve, and they
// name different paths, the env var's path wins. resolveBetterleaksPath has
// no injected seam of its own, so this plants the sibling next to this test
// binary's own real os.Executable() path -- the exact location production
// code's siblingBetterleaksPath would check -- and removes it afterward.
func TestResolveBetterleaksPath_EnvVarWinsOverSibling(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable(): %v", err)
	}
	sibling := filepath.Join(filepath.Dir(self), "betterleaks")
	if err := os.WriteFile(sibling, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Skipf("cannot plant a sibling next to the test binary in this environment: %v", err)
	}
	t.Cleanup(func() { os.Remove(sibling) })

	envBin := writeExecutableStub(t, t.TempDir(), "env-betterleaks")
	t.Setenv(betterleaksBinEnvVar, envBin)

	path, err := resolveBetterleaksPath()
	if err != nil {
		t.Fatalf("resolveBetterleaksPath() error = %v", err)
	}
	if path != envBin {
		t.Fatalf("resolveBetterleaksPath() = %q, want the env var's path %q (it must win over a resolving sibling)", path, envBin)
	}
}

// TestSiblingBetterleaksPathFrom_ResolvesWhenPresent proves the fallback
// resolves a file literally named betterleaks in the same directory as the
// injected self-path, when nothing else names it.
func TestSiblingBetterleaksPathFrom_ResolvesWhenPresent(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "git-tools")
	sibling := filepath.Join(dir, "betterleaks")
	if err := os.WriteFile(sibling, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	path, ok := siblingBetterleaksPathFrom(func() (string, error) { return self, nil })
	if !ok {
		t.Fatal("siblingBetterleaksPathFrom() ok = false, want true")
	}
	if path != sibling {
		t.Fatalf("siblingBetterleaksPathFrom() = %q, want %q", path, sibling)
	}
}

// TestSiblingBetterleaksPathFrom_MissingSiblingFailsClosed proves a missing
// sibling file fails closed with ok=false, not an error -- resolveBetterleaksPath
// treats this as "keep looking" (here, nowhere left to look), not as a fault.
func TestSiblingBetterleaksPathFrom_MissingSiblingFailsClosed(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "git-tools")

	_, ok := siblingBetterleaksPathFrom(func() (string, error) { return self, nil })
	if ok {
		t.Fatal("siblingBetterleaksPathFrom() ok = true with no sibling file present, want false")
	}
}

// TestSiblingBetterleaksPathFrom_SelfLookupFailureFailsClosed proves an
// os.Executable()-shaped failure also fails closed with ok=false, rather
// than panicking or propagating the error past resolveBetterleaksPath.
func TestSiblingBetterleaksPathFrom_SelfLookupFailureFailsClosed(t *testing.T) {
	_, ok := siblingBetterleaksPathFrom(func() (string, error) {
		return "", errors.New("os.Executable: boom")
	})
	if ok {
		t.Fatal("siblingBetterleaksPathFrom() ok = true despite a self-lookup failure, want false")
	}
}

// TestSiblingBetterleaksPathFrom_ResolvesThroughSymlinkedSelf proves a
// symlinked self-path still resolves the sibling correctly: the fallback
// must resolve the real directory first (filepath.EvalSymlinks), not the
// symlink's own directory, since betterleaks is provisioned next to
// git-tools' real file, not next to wherever a symlink to it happens to sit.
func TestSiblingBetterleaksPathFrom_ResolvesThroughSymlinkedSelf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevated privilege on Windows")
	}
	realDir := t.TempDir()
	linkDir := t.TempDir()

	realSelf := filepath.Join(realDir, "git-tools")
	if err := os.WriteFile(realSelf, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(realDir, "betterleaks")
	if err := os.WriteFile(sibling, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	linkedSelf := filepath.Join(linkDir, "git-tools")
	if err := os.Symlink(realSelf, linkedSelf); err != nil {
		t.Skipf("symlinks unsupported in this environment: %v", err)
	}

	path, ok := siblingBetterleaksPathFrom(func() (string, error) { return linkedSelf, nil })
	if !ok {
		t.Fatal("siblingBetterleaksPathFrom() ok = false, want true (should resolve through the symlink to the real directory)")
	}
	if path != sibling {
		t.Fatalf("siblingBetterleaksPathFrom() = %q, want the real sibling %q, not one relative to the symlink's own directory", path, sibling)
	}
}
