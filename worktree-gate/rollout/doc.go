// Package rollout implements the self-application guard for the
// worktree-isolation gate: a kill switch that keeps enforcement inert until
// an operator opts in from outside the session being guarded.
//
// A fail-closed, deny-capable gate has no way to un-deny itself once wired
// into the session that's building it -- a single false-positive can brick
// that session's own writes with no recourse. Resolve reads two explicit
// environment variables rather than one, so the risky half-configured state
// (enforcement requested without the isolation attestation) is a distinct,
// loud outcome -- SelfApplicationRisk -- rather than silently collapsing
// into either "on" or "off". Run then wraps detect.Run so a caller has one
// place to route every hook invocation through, regardless of Status.
package rollout
