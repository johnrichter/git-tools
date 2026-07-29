package rollout

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/johnrichter/git-tools/worktree-gate/detect"
)

const (
	// EnvVar requests enforcement. Any value other than exactly "1",
	// including unset, leaves enforcement off.
	EnvVar = "GIT_TOOLS_WORKTREE_GATE_ENFORCE"
	// ValidatedEnvVar attests that this rollout was validated in isolation
	// -- outside the session EnvVar is being set in. Required alongside
	// EnvVar for enforcement to actually turn on.
	ValidatedEnvVar = "GIT_TOOLS_WORKTREE_GATE_VALIDATED_ISOLATION"
)

// ForcedPauseExitCode is Run's exit code for SelfApplicationRisk: distinct
// from 0 (allow) and detect.Run's own 1 (response-encode failure), so a
// caller or CI step can tell "the call was allowed, but this rollout is
// misconfigured and needs attention" apart from a clean pass.
const ForcedPauseExitCode = 2

// Status is the rollout flag's resolved state.
type Status int

const (
	// Disabled is the default: enforcement is off, calls are only observed.
	Disabled Status = iota
	// Enabled means both EnvVar and ValidatedEnvVar are set: Run enforces.
	Enabled
	// SelfApplicationRisk means EnvVar is set without ValidatedEnvVar --
	// enforcement was requested but never attested as validated in
	// isolation. Forces a pause rather than defaulting to Disabled, since a
	// silent default here is exactly how enforcement gets flipped on inside
	// its own build session by accident.
	SelfApplicationRisk
)

func (s Status) String() string {
	switch s {
	case Enabled:
		return "enabled"
	case SelfApplicationRisk:
		return "self-application-risk"
	default:
		return "disabled"
	}
}

// Resolve reads getenv (os.Getenv in production) and returns the rollout's
// current Status.
func Resolve(getenv func(string) string) Status {
	requested := getenv(EnvVar) == "1"
	validated := getenv(ValidatedEnvVar) == "1"
	switch {
	case requested && validated:
		return Enabled
	case requested:
		return SelfApplicationRisk
	default:
		return Disabled
	}
}

// Run routes one PreToolUse hook invocation through the worktree gate under
// status:
//
//   - Enabled delegates to detect.Run unchanged: the real verdict is
//     enforced.
//   - Disabled still runs detect.Run, but against a discarded stdout --
//     any deny it would have produced is reported on errOut as an
//     observation, never enforced, so the call always proceeds.
//   - SelfApplicationRisk never runs Decide at all: the call proceeds and
//     errOut names the missing attestation, and the distinct
//     ForcedPauseExitCode signals a caller that this rollout needs
//     attention before it's retried.
//
// Every path other than Enabled therefore returns 0 to stdout callers (the
// tool call itself is never blocked) while still surfacing what happened on
// errOut and, for SelfApplicationRisk, on the exit code.
func Run(status Status, r io.Reader, stdout, errOut io.Writer, lstat detect.LstatFunc, readFile detect.ReadFileFunc, getenv func(string) string) int {
	switch status {
	case Enabled:
		return detect.Run(r, stdout, errOut, lstat, readFile, getenv)
	case SelfApplicationRisk:
		fmt.Fprintf(errOut,
			"worktree-gate: %s is set without %s -- forcing a pause: enforcement stays off until this rollout is validated in isolation, outside the session that set %s\n",
			EnvVar, ValidatedEnvVar, EnvVar)
		return ForcedPauseExitCode
	default: // Disabled
		var wouldDeny bytes.Buffer
		detect.Run(r, &wouldDeny, errOut, lstat, readFile, getenv)
		if wouldDeny.Len() > 0 {
			fmt.Fprintf(errOut, "worktree-gate: rollout disabled (%s unset) -- observed a deny this call did not enforce: %s\n",
				EnvVar, strings.TrimSpace(wouldDeny.String()))
		}
		return 0
	}
}
