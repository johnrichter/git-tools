package detect

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

// -- AC1: one decomposition, shared by the classifier and the cwd resolver --

type decompositionCase struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Pieces  []string `json:"pieces"`
}

func loadDecompositionCorpus(t *testing.T) []decompositionCase {
	t.Helper()
	b, err := os.ReadFile("testdata/decomposition-corpus.json")
	if err != nil {
		t.Fatalf("testdata/decomposition-corpus.json: %v", err)
	}
	var c struct {
		Cases []decompositionCase `json:"cases"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("testdata/decomposition-corpus.json: %v", err)
	}
	if len(c.Cases) == 0 {
		t.Fatal("decomposition corpus carries no cases")
	}
	return c.Cases
}

// decompose is the single shared decomposition; the golden pins the piece list
// it produces, so a future edit that splits differently for one consumer fails
// here rather than diverging the two silently.
func TestDecomposition_MatchesGoldenCorpus(t *testing.T) {
	for _, c := range loadDecompositionCorpus(t) {
		t.Run(c.Name, func(t *testing.T) {
			var got []string
			for _, p := range decompose(c.Command) {
				got = append(got, p.argv)
			}
			if !reflect.DeepEqual(got, c.Pieces) {
				t.Errorf("decompose(%q) argv = %#v, want %#v", c.Command, got, c.Pieces)
			}
		})
	}
}

// The cwd resolver reads its cd/-C target from the same redirect-delimited argv
// the classifier keys on: `cd /a>/b` composes /a, not the glued string, and a
// git invocation after any connector is still found. This is the observable
// consequence of both consumers sharing one connector set and one redirect
// predicate.
func TestDecomposition_CwdResolverConsumesTheSharedPieces(t *testing.T) {
	if dir, unresolvable := resolveEffectiveCWD("cd /a>/b/tracked && git commit"); unresolvable || dir != "/a" {
		t.Errorf("resolveEffectiveCWD(redirect-glued cd) = (%q, %v), want (/a, false)", dir, unresolvable)
	}
	for _, conn := range loadConnectorArtifact(t).Connectors {
		cmd := "cd /a" + conn + "git status"
		if dir, unresolvable := resolveEffectiveCWD(cmd); unresolvable || dir != "/a" {
			t.Errorf("resolveEffectiveCWD(%q) = (%q, %v), want (/a, false): resolver did not split on connector %q", cmd, dir, unresolvable, conn)
		}
	}
}

// -- AC2: the three connector sets are pinned SET-EQUAL to the artifact --

type connectorArtifact struct {
	Connectors []string `json:"connectors"`
}

func loadConnectorArtifact(t *testing.T) connectorArtifact {
	t.Helper()
	b, err := os.ReadFile("contracts/connectors.json")
	if err != nil {
		t.Fatalf("contracts/connectors.json: %v", err)
	}
	var a connectorArtifact
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatalf("contracts/connectors.json: %v", err)
	}
	if len(a.Connectors) == 0 {
		t.Fatal("connector artifact carries no connectors")
	}
	return a
}

func TestConnectorSet_PinnedToCanonicalArtifact(t *testing.T) {
	art := loadConnectorArtifact(t)

	// The one Go connector set (fed to both the classifier's decomposition and
	// the cwd resolver's) equals the canonical artifact.
	if !sameStringSet(bashConnectors, art.Connectors) {
		t.Fatalf("bashConnectors %#v not set-equal to contracts/connectors.json %#v", bashConnectors, art.Connectors)
	}

	// Each canonical connector actually splits both consumers, and a
	// non-connector does not -- so the set is exercised, not just compared.
	v := testVerbs(t)
	for _, conn := range art.Connectors {
		if pieces := decompose("aa" + conn + "bb"); len(pieces) != 2 {
			t.Errorf("decompose(aa%qbb) = %d pieces, want 2", conn, len(pieces))
		}
		if got := ClassifyBash(v, "git status"+conn+"rm -rf x"); got != ClassWrite {
			t.Errorf("classifier did not split on connector %q: ClassifyBash = %v, want ClassWrite", conn, got)
		}
	}
	if pieces := decompose("aa+bb"); len(pieces) != 1 {
		t.Errorf("decompose(aa+bb) = %d pieces, want 1 (+ is not a connector)", len(pieces))
	}
}

// The shell gate's sed pass is the third connector set. It lives in the
// marketplace repo, so it is pinned to this same canonical artifact by
// byte-equality against a copy the marketplace ships, opt-in when both repos
// are checked out (matching the cwd-corpus / trackingdocs contract checks).
func TestConnectorArtifact_ByteEqualsMarketplaceCanonicalCopy(t *testing.T) {
	ref := os.Getenv("MARKETPLACE_CONNECTORS")
	if ref == "" {
		t.Skip("MARKETPLACE_CONNECTORS not set; skipping the cross-repo connector-set byte-equality check")
	}
	assertByteEqual(t, "contracts/connectors.json", ref)
}

// -- AC3: redirect-operator delimiting is a longest-first predicate --

func TestRedirectOperator_LongestFirstPredicate(t *testing.T) {
	cases := []struct {
		op     string
		length int
		output bool
	}{
		{">", 1, true},
		{">>", 2, true},
		{">|", 2, true},
		{">&", 2, true},
		{"&>", 2, true},
		{"&>>", 3, true},
		{"<", 1, false},
		{"<&", 2, false},
		{"2>", 2, true},
		{"2>>", 3, true},
		{"2>&", 3, true},
		{"10>>", 4, true},
	}
	for _, c := range cases {
		length, output, ok := redirectOperatorAt(c.op, 0, true)
		if !ok || length != c.length || output != c.output {
			t.Errorf("redirectOperatorAt(%q) = (len=%d, output=%v, ok=%v), want (len=%d, output=%v, ok=true)",
				c.op, length, output, ok, c.length, c.output)
		}
	}
	// Longest-first: >> must not match as a bare > , and a numeric fd prefix
	// only binds at a word boundary.
	if length, _, _ := redirectOperatorAt(">>x", 0, true); length != 2 {
		t.Errorf("redirectOperatorAt(>>) matched %d, want the longest 2", length)
	}
	if _, _, ok := redirectOperatorAt("x2>f", 2, false); !ok {
		t.Error("expected the bare > in x2>f to redirect (the 2 is mid-word, not an fd prefix)")
	}
}

func TestRedirect_GluedTargetDelimitsAndWrites(t *testing.T) {
	v := testVerbs(t)
	if got := ClassifyBash(v, "echo x>/p/tracked"); got != ClassWrite {
		t.Errorf("ClassifyBash(glued redirect) = %v, want ClassWrite", got)
	}
	if got := ClassifyBash(v, "echo x&>>/p/tracked"); got != ClassWrite {
		t.Errorf("ClassifyBash(&>> redirect) = %v, want ClassWrite", got)
	}
}

// AC3: a post-operator fd-dup target is a non-path, so a read command with only
// an fd-dup redirect stays a read (git status 2>&1, echo x>&2), not a write.
func TestRedirect_FdDupExcludedAsNonPath(t *testing.T) {
	for _, w := range []string{"2", "&1", "-"} {
		if !isFdDupTarget(w) {
			t.Errorf("isFdDupTarget(%q) = false, want true", w)
		}
	}
	for _, w := range []string{"/p/f", "file", "&"} {
		if isFdDupTarget(w) {
			t.Errorf("isFdDupTarget(%q) = true, want false", w)
		}
	}
	v := testVerbs(t)
	for _, cmd := range []string{"git status 2>&1", "echo x>&2"} {
		if got := ClassifyBash(v, cmd); got != ClassRead {
			t.Errorf("ClassifyBash(%q) = %v, want ClassRead (fd-dup is not a write)", cmd, got)
		}
	}
}

// -- AC4: SC16 interior recursion, strictest verdict wins --

func TestSC16_InteriorRecursion_StrictestWins(t *testing.T) {
	v := testVerbs(t)
	backtickWrite := "echo \x60git commit -m x\x60"
	heredocWrite := "cat <<EOF\nrm -rf build\nEOF\n"
	writes := map[string]string{
		"command-substitution": "echo $(git commit -m x)",
		"eval":                 "eval 'git commit -m x'",
		"bash-c":               "bash -c 'git commit -m x'",
		"sh-c":                 "sh -c 'git commit -m x'",
		"backtick":             backtickWrite,
		"group":                "{ git commit -m x; }",
		"subshell":             "( git commit -m x )",
		"xargs":                "printf x | xargs rm -rf",
		"heredoc-body":         heredocWrite,
		"substitution-rm":      "echo $(rm -rf x)",
		"substitution-tee":     "echo $(tee x)",
		"eval-sed":             "eval 'sed -i s/a/b/ f'",
	}
	for name, cmd := range writes {
		t.Run(name, func(t *testing.T) {
			if got := ClassifyBash(v, cmd); got != ClassWrite {
				t.Errorf("ClassifyBash(%q) = %v, want ClassWrite (interior write must surface)", cmd, got)
			}
		})
	}
}

// AC5: the undecomposable case is bounded -- a here-document body with no
// governed command word falls through to D6 leniency and is not denied, while
// a real git commit with a quoted apostrophe classifies cleanly as a write
// (allowed only from inside a worktree, never spuriously denied on the quote).
func TestSC16_UndecomposableBoundedByGovernedWord(t *testing.T) {
	v := testVerbs(t)
	prose := "cat <<'EOF'\njust plain text here\nEOF\n"
	if got := ClassifyBash(v, prose); got != ClassRead {
		t.Errorf("ClassifyBash(quoted heredoc prose) = %v, want ClassRead (no governed word, D6 lenient)", got)
	}
	if got := ClassifyBash(v, `git commit -m "it's fine"`); got != ClassWrite {
		t.Errorf("ClassifyBash(git commit with embedded apostrophe) = %v, want ClassWrite (clean classify)", got)
	}
	// "not denied" end-to-end: the same clean git commit is allowed from a worktree.
	fs := worktreeFS()
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo/wt", Command: `git commit -m "it's fine"`,
	})
	if d.Deny {
		t.Errorf("git commit with an embedded apostrophe was denied from a worktree: %s", d.Reason)
	}
}

// A command trailing a heredoc operator on the same line (the operator-line
// tail) must still decompose and classify -- the body is pulled out, but the
// tail is not swallowed with it. A write in the tail surfaces as a write and
// is denied from a primary checkout; stacked heredocs and a command after a
// closed heredoc block behave the same.
func TestSC16_HeredocOperatorLineTailStillClassified(t *testing.T) {
	v := testVerbs(t)
	writes := map[string]string{
		"and-then-commit":     "cat <<EOF && git commit -m x\ndata\nEOF\n",
		"and-then-rm":         "cat <<EOF && rm -rf notes\ndata\nEOF\n",
		"semicolon-commit":    "cat <<EOF; git commit -m x\ndata\nEOF\n",
		"pipe-commit":         "cat <<EOF | git commit\nbody\nEOF\n",
		"stacked-then-rm":     "cat <<A <<B && rm -rf x\nbodyA\nA\nbodyB\nB\n",
		"command-after-block": "cat <<EOF\ndata\nEOF\ngit commit -m x\n",
	}
	for name, cmd := range writes {
		t.Run(name, func(t *testing.T) {
			if got := ClassifyBash(v, cmd); got != ClassWrite {
				t.Errorf("ClassifyBash(%q) = %v, want ClassWrite (operator-line tail must not be swallowed)", cmd, got)
			}
			fs := primaryFS()
			if d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: cmd,
			}); !d.Deny {
				t.Errorf("Decide(%q) from a primary checkout = allow, want deny", cmd)
			}
		})
	}
	// The body itself (no governed word) stays bounded: a prose heredoc with a
	// trailing read is still a read.
	if got := ClassifyBash(v, "cat <<'EOF'\njust text\nEOF\nls -la\n"); got == ClassWrite {
		t.Errorf("prose heredoc + trailing read misclassified as write")
	}
}

// -- AC6: SC22's cd skip fires only for exactly `cd <literal-target>` --

func TestSC22_CdSkipFiresOnlyForExactLiteralCdTarget(t *testing.T) {
	v := testVerbs(t)
	// The skip fires: a bare cd to a literal target writes nothing.
	if got := ClassifyBash(v, "cd /repo"); got != ClassRead {
		t.Errorf("ClassifyBash(cd /repo) = %v, want ClassRead (SC22 skip)", got)
	}
	// Boundary cases the skip must NOT swallow.
	notSkipped := map[string]string{
		"redirect-still-writes":  "cd /repo > /repo/tracked", // must stay ClassWrite
		"substitution-target":    "cd $(rm -rf x)",           // must stay ClassWrite (interior)
		"glob-target":            "cd /repo/*",
		"backtick-target":        "cd \x60pwd\x60",
		"extra-token":            "cd /a /b",
		"leading-var-assignment": "VAR=1 cd /repo",
	}
	for name, cmd := range notSkipped {
		t.Run(name, func(t *testing.T) {
			pieces := decompose(cmd)
			if eligibleCdSkip(pieces[0]) {
				t.Errorf("eligibleCdSkip(%q) = true, want false (ineligible piece must not be skipped)", cmd)
			}
		})
	}
	// The two write boundaries specifically stay ClassWrite.
	if got := ClassifyBash(v, "cd /repo > /repo/tracked"); got != ClassWrite {
		t.Errorf("ClassifyBash(cd with redirect) = %v, want ClassWrite", got)
	}
	if got := ClassifyBash(v, "cd $(rm -rf x)"); got != ClassWrite {
		t.Errorf("ClassifyBash(cd with substitution) = %v, want ClassWrite", got)
	}
}

// The standing invariant behind SC22's eligibility gate: no write_contains
// entry is reachable inside an eligible cd piece other than as an inert
// substring of the literal target. Redirect operators are excluded by the
// eligibility gate; a find predicate can only appear inside the path, where a
// bare cd still writes nothing.
func TestSC22_NoWriteContainsReachableInEligiblePiece(t *testing.T) {
	v := testVerbs(t)
	for _, w := range v.WriteContains {
		if isRedirectOperatorString(w) {
			continue // redirects disqualify eligibility outright
		}
		cmd := "cd /tmp/a" + w + "b"
		pieces := decompose(cmd)
		if !eligibleCdSkip(pieces[0]) {
			t.Errorf("cd piece embedding %q was not eligible; expected the write_contains entry to be an inert substring of the literal target", w)
		}
		if got := ClassifyBash(v, cmd); got != ClassRead {
			t.Errorf("ClassifyBash(%q) = %v, want ClassRead: %q must be inert inside a literal cd target", cmd, got, w)
		}
	}
}

// The classifier grants NO VAR=value eligibility, while the cwd resolver still
// composes a VAR=-prefixed non-leading cd (the resolver's tolerance is wider on
// purpose). So `VAR=1 cd <primary> && <cli> merge <b>` is a false deny, never
// an allowance.
func TestSC22_ClassifierHasNoVarSkipButResolverComposes(t *testing.T) {
	if dir, unresolvable := resolveEffectiveCWD("VAR=1 cd /srv && git commit"); unresolvable || dir != "/srv" {
		t.Errorf("resolveEffectiveCWD(VAR= cd) = (%q, %v), want (/srv, false): resolver must still compose", dir, unresolvable)
	}
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo", Command: "VAR=1 cd /repo && /prov/git-tools merge b",
	})
	if !d.Deny {
		t.Errorf("VAR=1 cd <primary> && git-tools merge must deny (classifier grants no VAR= skip); got allow")
	}
}

// -- AC2 regression: the classifier gained the lone & connector --

func TestConnectorRegression_LoneAmpersandDeniesViaDecide(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	for _, cmd := range []string{"echo hi & git commit -m x", "echo hi & rm -rf notes"} {
		d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
			ToolName: "Bash", CWD: "/repo", Command: cmd,
		})
		if !d.Deny {
			t.Errorf("Decide(%q) from a primary checkout = allow, want deny (lone & backgrounds a write past the connector split)", cmd)
		}
	}
}

// -- AC7: contract artifacts are valid, deterministically-sorted JSON --

func TestContractArtifacts_ValidAndDeterministicallySorted(t *testing.T) {
	connectors := loadConnectorArtifact(t).Connectors
	if !sort.StringsAreSorted(connectors) {
		t.Errorf("contracts/connectors.json connectors not deterministically sorted: %#v", connectors)
	}

	// The banned-name set's specific membership and cross-repo enforcement are
	// owned by the SC10/SC11 grep gate, which consumes this artifact; here the
	// artifact is validated as well-formed: valid JSON, non-empty, unique,
	// deterministically sorted, and env-name shaped. Its contents are asserted
	// only through the file, never hardcoded here, so this source stays clean
	// of the very names the grep gate forbids in code.
	b, err := os.ReadFile("contracts/banned-names.json")
	if err != nil {
		t.Fatalf("contracts/banned-names.json: %v", err)
	}
	var banned struct {
		BannedNames []string `json:"banned_names"`
	}
	if err := json.Unmarshal(b, &banned); err != nil {
		t.Fatalf("contracts/banned-names.json: %v", err)
	}
	if len(banned.BannedNames) == 0 {
		t.Fatal("banned-names.json carries no names")
	}
	if !sort.StringsAreSorted(banned.BannedNames) {
		t.Errorf("banned-names.json not deterministically sorted: %#v", banned.BannedNames)
	}
	seen := map[string]bool{}
	for _, name := range banned.BannedNames {
		if seen[name] {
			t.Errorf("banned-names.json has a duplicate: %q", name)
		}
		seen[name] = true
		if !envNameShaped(name) {
			t.Errorf("banned-names.json entry %q is not an env-name shape (A-Z, 0-9, _)", name)
		}
	}
}

func envNameShaped(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
