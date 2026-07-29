package rollout

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/johnrichter/git-tools/worktree-gate/detect"
)

// EnvVar opts *out* of enforcement: enforcement is on by default (working
// outside a worktree is denied), matching the platform-wide rule that a
// user disables a feature they don't want rather than enabling one they do.
// Only the exact value "0" opts out; unset, "1", or anything else leaves
// enforcement on.
const EnvVar = "GIT_TOOLS_WORKTREE_GATE_ENFORCE"

// Status is the rollout flag's resolved state.
type Status int

const (
	// Enabled is the default: enforcement is on, calls are denied.
	Enabled Status = iota
	// Disabled means EnvVar is explicitly "0": enforcement is off, calls
	// are only observed.
	Disabled
)

func (s Status) String() string {
	if s == Disabled {
		return "disabled"
	}
	return "enabled"
}

// Resolve reads getenv (os.Getenv in production) and returns the rollout's
// current Status.
func Resolve(getenv func(string) string) Status {
	if getenv(EnvVar) == "0" {
		return Disabled
	}
	return Enabled
}

// Run routes one PreToolUse hook invocation through the worktree gate under
// status:
//
//   - Enabled delegates to detect.Run unchanged: the real verdict is
//     enforced.
//   - Disabled still runs detect.Run, but against a discarded stdout --
//     any deny it would have produced is reported on errOut as an
//     observation, never enforced, so the call always proceeds.
//
// Every path other than Enabled therefore returns 0 to stdout callers (the
// tool call itself is never blocked) while still surfacing what happened on
// errOut.
func Run(status Status, r io.Reader, stdout, errOut io.Writer, lstat detect.LstatFunc, readFile detect.ReadFileFunc, getenv func(string) string) int {
	switch status {
	case Enabled:
		return detect.Run(r, stdout, errOut, lstat, readFile, getenv)
	default: // Disabled
		var wouldDeny bytes.Buffer
		detect.Run(r, &wouldDeny, errOut, lstat, readFile, getenv)
		if wouldDeny.Len() > 0 {
			fmt.Fprintf(errOut, "worktree-gate: rollout disabled (%s=0) -- observed a deny this call did not enforce: %s\n",
				EnvVar, strings.TrimSpace(wouldDeny.String()))
		}
		return 0
	}
}
