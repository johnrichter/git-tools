package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitSpawnShapes lists every way a file in this module can hand git a
// subcommand to run, and where that call's git words sit in its arguments.
// All three matter: reading only the gitexec wrapper would leave a new `git
// tag` invisible in worktree-gate -- the very package whose backup markers
// D1 moved off tags -- because that package spawns git through its own
// private runner instead.
var gitSpawnShapes = []struct {
	// marker is the call text to scan for, its own "(" included.
	marker string
	// sliceArg is true when the git words arrive as one []string{...}
	// argument (sysops.Run's shape) rather than as a variadic tail
	// (gitexec.RunGit's and worktree-gate's runGit's shape).
	sliceArg bool
}{
	{marker: "gitexec.RunGit(", sliceArg: false},
	{marker: "runGit(", sliceArg: false},
	{marker: "sysops.Run(", sliceArg: true},
}

// TestRawGitTagCallSite_ConfinedToTagGo locks D1's migration in place: every
// call site that used to mint or read a backup marker as a `git tag` was
// moved onto go/git's BackupRef, leaving tag.go's own "create" verb as the
// sole place in the module that still shells out to the raw git tag
// subcommand (it uses three forms of it -- create, verify, rollback -- but
// all three belong to the one verb). A call anywhere else naming "tag" as
// the subcommand it spawns would mean some other verb started minting or
// reading tags directly again, bypassing that migration -- exactly what this
// guard exists to catch.
//
// It reads the subcommand words of every git spawn shape in gitSpawnShapes,
// not just the one wrapper internal/cli happens to use, and flags a literal
// "tag" among them wherever it appears in those words rather than only in
// first position -- deliberately over-flagging, since a `git push origin tag
// v1` is also a site that mints or moves a tag and should come up for review.
//
// Scope, so a reader does not over-trust it: this is a source-text check for
// a literal "tag" argument. A call that builds its argument slice into a
// variable first and passes that (as scan.go's own listCandidates does) is
// invisible here, and so is a git spawn through a fourth shape nobody has
// added to gitSpawnShapes yet -- which is why the shapes are self-checked
// below. It catches the casual regression -- someone typing "tag" as a
// subcommand in a new verb -- not a determined evasion, which would need
// type-checked call-graph analysis.
func TestRawGitTagCallSite_ConfinedToTagGo(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var sites []string
	// Per-shape call counts, so a shape whose marker has gone stale (its
	// runner renamed, its package retired) fails loudly instead of quietly
	// checking nothing forever.
	callsPerShape := make(map[string]int, len(gitSpawnShapes))
	walkErr := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			// .claude holds this repo's agent worktrees, each a full
			// checkout of some other branch: walking into them would judge
			// this branch by another branch's call sites.
			case ".git", ".claude", ".task-reports", ".dat":
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
		for _, shape := range gitSpawnShapes {
			for _, call := range extractCalls(string(src), shape.marker) {
				callsPerShape[shape.marker]++
				for _, arg := range gitSubcommandWords(call, shape.sliceArg) {
					if strings.TrimSpace(arg) == `"tag"` {
						sites = append(sites, path)
					}
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	for _, shape := range gitSpawnShapes {
		if callsPerShape[shape.marker] == 0 {
			t.Fatalf("no %s call site left in the module -- this guard's shape list is stale, update gitSpawnShapes", shape.marker)
		}
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

// gitSubcommandWords returns the arguments a git spawn passes to git, given
// the call's argument text and whether that spawn's shape carries its words
// in a slice argument. Both shapes put a context first and the spawn's
// target directory or program name second, so the words start at the third
// argument either way. It returns nil when the call cannot be a git spawn at
// all (sysops.Run of some other program, say) or when the words are not a
// literal this check can read.
func gitSubcommandWords(call string, sliceArg bool) []string {
	args := splitTopLevelArgs(call)
	if len(args) < 3 {
		return nil
	}
	if !sliceArg {
		return args[2:]
	}
	// sysops.Run(ctx, name, args, options): only a git spawn is ours to
	// judge, and its words are the elements of the args slice literal.
	if strings.TrimSpace(args[1]) != `"git"` {
		return nil
	}
	return sliceLiteralElements(args[2])
}

// sliceLiteralElements splits a []string{...} composite literal into its
// element texts, returning nil for an argument that is not one -- a variable
// holding the elements is beyond what a source-text check can follow.
func sliceLiteralElements(arg string) []string {
	arg = strings.TrimSpace(arg)
	const prefix = "[]string{"
	if !strings.HasPrefix(arg, prefix) || !strings.HasSuffix(arg, "}") {
		return nil
	}
	return splitTopLevelArgs(arg[len(prefix) : len(arg)-1])
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
