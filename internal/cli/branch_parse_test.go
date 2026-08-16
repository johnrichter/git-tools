// Unit tests for listLocalBranchesRows, the pure parse extracted from
// listLocalBranches. It is unexported, so these live in package cli
// (white-box) alongside the payload-only fixtures a scratch git repo can't
// exercise directly -- the CLI-level scratch-repo case lives in
// branch_test.go instead.
package cli

import "testing"

func TestListLocalBranchesRows(t *testing.T) {
	const (
		mainSHA    = "1111111111111111111111111111111111111111"
		featureSHA = "2222222222222222222222222222222222222222"
	)

	cases := []struct {
		name string
		out  string
		want []branchEntry
	}{
		{
			name: "last row is a non-HEAD branch",
			// Before the fix, a whole-output TrimSpace stripped this row's
			// trailing tab along with the final newline, leaving it with two
			// fields and panicking on fields[2] -- this is the failing-first
			// case: it panics on the pre-fix code and passes after.
			out:  "main\t" + mainSHA + "\t*\nfeature\t" + featureSHA + "\t\n",
			want: []branchEntry{{Name: "main", Head: mainSHA, Current: true}, {Name: "feature", Head: featureSHA, Current: false}},
		},
		{
			name: "last row is the HEAD branch",
			out:  "feature\t" + featureSHA + "\t\nmain\t" + mainSHA + "\t*\n",
			want: []branchEntry{{Name: "feature", Head: featureSHA, Current: false}, {Name: "main", Head: mainSHA, Current: true}},
		},
		{
			name: "single branch",
			out:  "main\t" + mainSHA + "\t*\n",
			want: []branchEntry{{Name: "main", Head: mainSHA, Current: true}},
		},
		{
			name: "empty output",
			out:  "",
			want: nil,
		},
		{
			name: "trailing newline and no trailing newline agree",
			out:  "main\t" + mainSHA + "\t*",
			want: []branchEntry{{Name: "main", Head: mainSHA, Current: true}},
		},
		{
			name: "branch name containing a space",
			out:  "release/2024 q1\t" + mainSHA + "\t*\n",
			want: []branchEntry{{Name: "release/2024 q1", Head: mainSHA, Current: true}},
		},
		{
			name: "row missing the third field entirely",
			out:  "feature\t" + featureSHA + "\n",
			want: []branchEntry{{Name: "feature", Head: featureSHA, Current: false}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := listLocalBranchesRows(tc.out)
			if len(got) != len(tc.want) {
				t.Fatalf("listLocalBranchesRows(%q) = %+v, want %+v", tc.out, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("entry %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestListLocalBranchesRows_TrailingNewlineIsOptional pins AC3 directly:
// the same rows, with and without a trailing newline, must parse to
// identical entries -- trimming is explicit about what it removes, not
// incidental to a broader whitespace trim.
func TestListLocalBranchesRows_TrailingNewlineIsOptional(t *testing.T) {
	const sha = "3333333333333333333333333333333333333333"
	withNewline := "main\t" + sha + "\t*\n"
	withoutNewline := "main\t" + sha + "\t*"

	got := listLocalBranchesRows(withNewline)
	want := listLocalBranchesRows(withoutNewline)
	if len(got) != 1 || len(want) != 1 || got[0] != want[0] {
		t.Fatalf("trailing-newline and no-trailing-newline parses differ: %+v vs %+v", got, want)
	}
}
