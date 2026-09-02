// Trip-wire for the one cross-package contract this CLI's flag surface owes
// the worktree-gate: the gate's own gitToolsValueOptions table (see
// worktree-gate/detect/decide.go) is a hand-maintained mirror of which flags
// here consume the FOLLOWING token as their value. The gate has no import of
// this package and no way to ask, so nothing but this test stands between a
// new value-taking flag and two silent classification failures:
//
//   - SC15's landing-verb checks skip leading flags to find the verb word. A
//     root persistent flag missing from the table is skipped as if valueless,
//     so its value is read as the verb -- and `--newflag merge …` would hand
//     the write exemption to whatever cobra actually dispatches instead.
//   - SC20 resolves a write verb's path operand the same way, so a missing
//     entry shifts that operand out of the named-path rule's view.
//
// Both fail in the ALLOW direction and neither shows up in the gate's own
// tests, which cannot see this package. So this test guards the producer side:
// it fails the moment the flag surface it pins moves, whichever way it moves.
// A failure here is not a defect in this package -- it means the gate's table
// needs the same edit before the change ships.
package cli

import (
	"sort"
	"testing"

	"github.com/spf13/pflag"
)

// consumesFollowingToken reports whether f must be given a value as the next
// token. pflag leaves NoOptDefVal empty for exactly those flags; a bool (or
// any flag spellable bare) carries one.
func consumesFollowingToken(f *pflag.Flag) bool { return f.NoOptDefVal == "" }

func valueTakingFlags(fs *pflag.FlagSet) []string {
	var names []string
	fs.VisitAll(func(f *pflag.Flag) {
		if consumesFollowingToken(f) {
			names = append(names, "--"+f.Name)
		}
	})
	sort.Strings(names)
	return names
}

func TestRootFlagSurface_MirrorsTheGatesValueOptionTable(t *testing.T) {
	root := newRootCmd()

	// The root's persistent flags reach every verb and may be spelled on
	// either side of the verb word, which is what makes them the set the
	// gate's leading-flag skip depends on.
	if got, want := valueTakingFlags(root.PersistentFlags()), []string{
		"--config", "--max-binary-bytes", "--privacy-tier", "--remote", "--repo",
	}; !equalStrings(got, want) {
		t.Errorf("root persistent value-taking flags = %v, want %v -- add or remove the same entry in the gate's gitToolsValueOptions", got, want)
	}

	// The landing verbs whose own operands SC20 resolves as write
	// destinations. Their own flags cannot legally precede the verb, but they
	// sit among the operands the gate skips to reach the path.
	for _, c := range []struct {
		path []string
		want []string
	}{
		{[]string{"worktree", "add"}, []string{"--branch"}},
		{[]string{"worktree", "remove"}, []string{"--landing-target"}},
		{[]string{"branch", "delete"}, []string{"--landing-target"}},
	} {
		cmd, _, err := root.Find(c.path)
		if err != nil {
			t.Fatalf("Find(%v): %v", c.path, err)
		}
		if got := valueTakingFlags(cmd.LocalNonPersistentFlags()); !equalStrings(got, c.want) {
			t.Errorf("%v value-taking flags = %v, want %v -- add or remove the same entry in the gate's gitToolsValueOptions", c.path, got, c.want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
