package detect

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRun_DeniesWriteOutsideWorktree(t *testing.T) {
	fs := primaryFS()
	in := strings.NewReader(`{"tool_name":"Write","tool_input":{"file_path":"/repo/a.go"}}`)
	var out, errOut bytes.Buffer

	code := Run(in, &out, &errOut, fs.lstat, fs.readFile, noEnv)

	if code != 0 {
		t.Fatalf("Run() exit code = %d, want 0 (the deny is carried in stdout JSON, not the exit code)", code)
	}
	var resp response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("stdout did not decode as a hook response: %v (stdout=%q)", err, out.String())
	}
	if resp.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("permissionDecision = %q, want deny", resp.HookSpecificOutput.PermissionDecision)
	}
}

func TestRun_AllowsWriteInsideWorktree_NoOutput(t *testing.T) {
	fs := worktreeFS()
	in := strings.NewReader(`{"tool_name":"Write","tool_input":{"file_path":"/repo/wt/a.go"}}`)
	var out, errOut bytes.Buffer

	code := Run(in, &out, &errOut, fs.lstat, fs.readFile, noEnv)

	if code != 0 || out.Len() != 0 {
		t.Errorf("Run() = code=%d stdout=%q, want code=0 and no stdout for an allowed call", code, out.String())
	}
}

func TestRun_DeniesUncertainBashInPrimaryCheckout(t *testing.T) {
	fs := primaryFS()
	in := strings.NewReader(`{"tool_name":"Bash","cwd":"/repo","tool_input":{"command":"some-unlisted-tool"}}`)
	var out, errOut bytes.Buffer

	Run(in, &out, &errOut, fs.lstat, fs.readFile, noEnv)

	var resp response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("expected a deny response for an uncertain Bash command, got stdout=%q err=%v", out.String(), err)
	}
	if resp.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("permissionDecision = %q, want deny", resp.HookSpecificOutput.PermissionDecision)
	}
}

func TestRun_ReadBashNeverTrips(t *testing.T) {
	fs := primaryFS()
	in := strings.NewReader(`{"tool_name":"Bash","cwd":"/repo","tool_input":{"command":"git status"}}`)
	var out, errOut bytes.Buffer

	code := Run(in, &out, &errOut, fs.lstat, fs.readFile, noEnv)

	if code != 0 || out.Len() != 0 {
		t.Errorf("Run() = code=%d stdout=%q, want no-op for a read command", code, out.String())
	}
}

func TestRun_UnrecognizedToolIsNoOp(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Read","tool_input":{"file_path":"/repo/a.go"}}`)
	var out, errOut bytes.Buffer
	fs := primaryFS()

	code := Run(in, &out, &errOut, fs.lstat, fs.readFile, noEnv)

	if code != 0 || out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("Run() on an ungoverned tool = code=%d stdout=%q stderr=%q, want a silent no-op", code, out.String(), errOut.String())
	}
}

func TestRun_UnparseablePayloadIsNoOp(t *testing.T) {
	in := strings.NewReader(`not json`)
	var out, errOut bytes.Buffer
	code := Run(in, &out, &errOut, primaryFS().lstat, primaryFS().readFile, noEnv)

	if code != 0 || out.Len() != 0 {
		t.Errorf("Run() on an unparseable payload = code=%d stdout=%q, want a silent no-op", code, out.String())
	}
}

// -- Run: the environment override is read here, not in Decide, so its
// resolution is proven end to end.

func envWith(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestRun_ProjectDirEnvVar_FeedsTrackingDocExemption(t *testing.T) {
	fs := newFakeFS().dir("/proj/.dat/some-effort/.git")
	in := strings.NewReader(`{"tool_name":"Write","tool_input":{"file_path":"/proj/.dat/some-effort/plan.json"}}`)
	var out, errOut bytes.Buffer

	code := Run(in, &out, &errOut, fs.lstat, fs.readFile, envWith(map[string]string{ProjectDirEnvVar: "/proj"}))

	if code != 0 || out.Len() != 0 {
		t.Errorf("Run() = code=%d stdout=%q, want an allowed tracking-doc write under the project dir", code, out.String())
	}
}
