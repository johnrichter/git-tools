// Package fixtures is the adversarial acceptance suite for the
// worktree-isolation gate (SC-WORKTREE): a declared set of write, read, and
// uncertain fixtures, plus a harness that runs them through detect.Decide.
//
// The three categories are the invariant itself: every write-fixture must
// deny, every read-fixture must never deny, and every uncertain-fixture
// must deny (fail closed, per detect's own conservative-over-approximation
// design). Verify proves the gate against the real classifier; VerifyWith
// lets the suite prove itself sensitive to a regression by swapping in a
// decider standing in for one.
package fixtures
