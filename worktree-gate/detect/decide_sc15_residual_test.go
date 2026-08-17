package detect

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// TestDecide_Bash_OQ19_Residual_Disclosed asserts, rather than closes, OQ19's
// residual: a ClassUncertain piece run from a cwd outside any repo stays
// ALLOWED even when it names a primary-checkout path inside opaque text the
// gate does not classify. Widening the named-path rule to the uncertain class
// would deny every ordinary `python3 script.py <path>` and `./opaque.sh`, so
// the hole is disclosed here on the worktree axis (R4/SC9's indirection
// residual), not silently relied on.
func TestDecide_Bash_OQ19_Residual_Disclosed(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git")
	v := testVerbs(t)
	cases := []struct {
		name    string
		command string
	}{
		{"python-c-writing-primary-path-from-outside-any-repo", `cd /tmp && python3 -c "open('/repo/f','w')"`},
		{"opaque-script-from-outside-any-repo", "cd /tmp && ./opaque.sh"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Decide(fs.lstat, fs.readFile, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: c.command,
			})
			if d.Deny {
				t.Fatalf("OQ19 residual: %q from a cwd outside any repo must stay ALLOWED (disclosed, not closed); got deny: %s", c.command, d.Reason)
			}
		})
	}
}

// recordingEnv is a getenv that records every key queried, so a test can prove
// the SC15 decision reads no environment: the path and digest arrive as argv.
type recordingEnv struct{ queried []string }

func (r *recordingEnv) get(key string) string {
	r.queried = append(r.queried, key)
	return ""
}

func sc15MergePayload() string {
	return `{"tool_name":"Bash","cwd":"/repo","tool_input":{"command":"/plugin-data/bin/git-tools merge main"}}`
}

// TestRunGate_SC15Allow_ReadsNoEnvironment proves the SC15 ALLOW is reached
// through argv alone: RunGate is handed the verified path and expected digest
// as parameters, every environment lookup returns empty, and the merge from a
// primary checkout is allowed with no output. It also pins the no-regression
// bound -- no environment override of the deleted merge-gate shape gates the
// decision: the gate queries no environment key at all.
func TestRunGate_SC15Allow_ReadsNoEnvironment(t *testing.T) {
	fs := newFakeFS().
		dir("/repo/.git").
		file("/plugin-data/bin/git-tools", "PROVISIONED-CLI-BYTES")
	sum := sha256.Sum256([]byte("PROVISIONED-CLI-BYTES"))
	digest := hex.EncodeToString(sum[:])

	env := &recordingEnv{}
	var out, errOut bytes.Buffer
	code := RunGate(strings.NewReader(sc15MergePayload()), &out, &errOut, fs.lstat, fs.readFile, env.get, "/plugin-data/bin/git-tools", digest)

	if code != 0 || out.Len() != 0 {
		t.Fatalf("SC15 merge from a primary checkout: code=%d stdout=%q, want a silent allow", code, out.String())
	}
	// A binary-selecting override of any shape would show up here as a queried
	// key. That no such override name exists in source is the SC11 grep gate's job.
	if len(env.queried) != 0 {
		t.Errorf("SC15 decision queried environment variables %v; the allowance must key on argv only", env.queried)
	}
}

// TestRunGate_SC15_ArgvAbsence_Denies asserts the gate denies the very merge
// it would otherwise allow when either argv parameter is missing -- absence of
// the path, or of the digest, is not the allowance.
func TestRunGate_SC15_ArgvAbsence_Denies(t *testing.T) {
	fs := newFakeFS().
		dir("/repo/.git").
		file("/plugin-data/bin/git-tools", "PROVISIONED-CLI-BYTES")
	sum := sha256.Sum256([]byte("PROVISIONED-CLI-BYTES"))
	digest := hex.EncodeToString(sum[:])

	cases := []struct {
		name   string
		path   string
		digest string
	}{
		{"no-path-parameter", "", digest},
		{"no-digest-parameter", "/plugin-data/bin/git-tools", ""},
		{"neither-parameter", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			RunGate(strings.NewReader(sc15MergePayload()), &out, &errOut, fs.lstat, fs.readFile, noEnv, c.path, c.digest)
			if out.Len() == 0 {
				t.Fatalf("SC15 merge with %s must DENY (argv parameter absent), got a silent allow", c.name)
			}
		})
	}
}

// TestDecideSource_ReadsNoEnvironment is the compile-out companion to the
// no-environment assertion above: the decision layer's own source names no
// environment-read primitive at all, so the SC15 path and digest cannot come
// from anywhere but argv.
func TestDecideSource_ReadsNoEnvironment(t *testing.T) {
	b, err := os.ReadFile("decide.go")
	if err != nil {
		t.Fatalf("decide.go: %v", err)
	}
	for _, forbidden := range []string{"os.Getenv", "os.Environ", "os.LookupEnv"} {
		if strings.Contains(string(b), forbidden) {
			t.Errorf("decide.go references %q; the decision layer must read no environment", forbidden)
		}
	}
}
