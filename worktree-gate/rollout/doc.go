// Package rollout implements the rollout flag for the worktree-isolation
// gate: enforcement is on by default, with a single explicit env var to
// opt out.
//
// Resolve reads EnvVar and returns Enabled unless it is set to exactly "0".
// Run then wraps detect.Run so a caller has one place to route every hook
// invocation through, regardless of Status: Enabled enforces detect.Run's
// verdict directly; Disabled still runs it but only reports what it would
// have denied, on errOut, without blocking the call.
package rollout
