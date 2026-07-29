package detect

import (
	"encoding/json"
	"fmt"
	"io"
)

// payload is the subset of a PreToolUse hook payload this gate reads.
// Unrecognized fields are ignored, matching every other consumer of this
// hook contract.
type payload struct {
	ToolName  string `json:"tool_name"`
	CWD       string `json:"cwd"`
	ToolInput struct {
		FilePath string `json:"file_path"`
		Command  string `json:"command"`
	} `json:"tool_input"`
}

type hookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

type response struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

// Run implements the PreToolUse gate as a hook process: it reads one
// payload from r, decides via Decide, and writes a deny response to
// stdout only when denying -- an allowed call produces no output, so the
// tool proceeds under the harness's default. A degraded (fail-open)
// classifier defect is reported on errOut only, never on stdout, so it
// never becomes part of the tool-call decision. getenv resolves the
// environment signals Decide's allow-list overrides read (os.Getenv in
// production). It returns the process exit code the caller should use.
func Run(r io.Reader, stdout, errOut io.Writer, lstat LstatFunc, readFile ReadFileFunc, getenv func(string) string) int {
	var p payload
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		// An unparseable payload isn't this gate's call to make.
		return 0
	}
	switch p.ToolName {
	case "Write", "Edit", "Bash":
	default:
		return 0
	}

	verbs, verbsErr := DefaultVerbs()
	trackingDocs, trackingDocsErr := DefaultTrackingDocs()
	decision := Decide(lstat, readFile, verbs, verbsErr, trackingDocs, trackingDocsErr, Input{
		ToolName:         p.ToolName,
		CWD:              p.CWD,
		FilePath:         p.ToolInput.FilePath,
		Command:          p.ToolInput.Command,
		ProjectDir:       getenv(ProjectDirEnvVar),
		MergeGateEnabled: getenv(MergeGateEnvVar) == "1",
	})

	if decision.Degraded != "" {
		fmt.Fprintf(errOut, "worktree-gate: classifier degraded, failing open: %s\n", decision.Degraded)
	}
	if !decision.Deny {
		return 0
	}

	out := response{HookSpecificOutput: hookSpecificOutput{
		HookEventName:            "PreToolUse",
		PermissionDecision:       "deny",
		PermissionDecisionReason: decision.Reason,
	}}
	if err := json.NewEncoder(stdout).Encode(out); err != nil {
		fmt.Fprintf(errOut, "worktree-gate: failed to encode decision: %v\n", err)
		return 1
	}
	return 0
}
