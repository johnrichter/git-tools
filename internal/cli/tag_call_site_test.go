package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRawGitTagCallSite_ConfinedToTagGo locks D1's migration in place: every
// call site that used to mint or read a backup marker as a `git tag` was
// moved onto go/git's BackupRef, leaving tag.go's own "create" verb as the
// sole place in the module that still shells out to the raw git tag
// subcommand (it uses three forms of it -- create, verify, rollback -- but
// all three belong to the one verb). A gitexec.RunGit call elsewhere passing
// "tag" as its subcommand would mean some other verb started minting or
// reading tags directly again, bypassing that migration -- exactly what this
// guard exists to catch.
func TestRawGitTagCallSite_ConfinedToTagGo(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var sites []string
	walkErr := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", ".task-reports", ".dat":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, call := range extractCalls(string(src), "gitexec.RunGit(") {
			for _, arg := range splitTopLevelArgs(call) {
				if strings.TrimSpace(arg) == `"tag"` {
					sites = append(sites, path)
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	if len(sites) == 0 {
		t.Fatal("found no raw `git tag` call site to check -- the guard itself is broken")
	}
	for _, site := range sites {
		if !strings.HasSuffix(site, filepath.Join("internal", "cli", "tag.go")) {
			t.Fatalf("raw `git tag` call site outside tag.go: %s", site)
		}
	}
}

// splitTopLevelArgs splits a balanced-paren call's argument text (as
// extractCalls returns it) on its top-level commas, skipping any comma
// nested inside a paren/bracket/brace or a quoted string literal.
func splitTopLevelArgs(args string) []string {
	var out []string
	depth := 0
	inString := false
	escaped := false
	start := 0
	for i, r := range args {
		switch {
		case inString:
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == '"':
				inString = false
			}
		case r == '"':
			inString = true
		case r == '(' || r == '[' || r == '{':
			depth++
		case r == ')' || r == ']' || r == '}':
			depth--
		case r == ',' && depth == 0:
			out = append(out, args[start:i])
			start = i + 1
		}
	}
	out = append(out, args[start:])
	return out
}
