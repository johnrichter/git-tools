package fixtures

import (
	"path/filepath"

	"github.com/johnrichter/git-tools/worktree-gate/detect"
)

// Category names the invariant one Case proves against Decide.
type Category string

const (
	// Write is a known repo-modifying call outside a worktree: Decide must
	// always deny it.
	Write Category = "write"
	// Read is a call that's either confirmed non-modifying or confidently
	// out of the gate's scope: Decide must never deny it.
	Read Category = "read"
	// Uncertain is a call whose repo topology or intent can't be resolved:
	// Decide must deny it (fail closed).
	Uncertain Category = "uncertain"
)

// Topology names a canned `.git`-entry layout a Case is evaluated against.
type Topology string

const (
	Primary       Topology = "primary"       // the repository's own primary checkout
	WorktreeEntry Topology = "worktree"      // a correctly linked worktree
	NoRepo        Topology = "no-repo"       // confidently outside any repository
	Indeterminate Topology = "indeterminate" // a `.git` entry present but unclassifiable
	FSErr         Topology = "fs-error"      // a filesystem error other than not-exist during the walk
)

// Case is one adversarial fixture: a PreToolUse call evaluated against a
// canned repository topology.
type Case struct {
	Name     string
	Category Category
	Topology Topology
	Tool     string // "Write", "Edit", or "Bash"
	FilePath string // Write/Edit target
	CWD      string // Bash working directory
	Command  string // Bash command

	ProjectDir       string // CLAUDE_PROJECT_DIR, feeds the tracking-doc exemption
	MergeGateEnabled bool   // DAT_MERGE_GATE == "1", feeds the landing-merge override
}

// wantDeny is the invariant Category encodes: only Read fixtures may pass
// through the gate.
func (c Case) wantDeny() bool { return c.Category != Read }

func (c Case) toInput() detect.Input {
	return detect.Input{
		ToolName: c.Tool, CWD: c.CWD, FilePath: c.FilePath, Command: c.Command,
		ProjectDir: c.ProjectDir, MergeGateEnabled: c.MergeGateEnabled,
	}
}

// anchorDir is where buildFS plants the case's `.git` entry: the Write/Edit
// target's directory, or the Bash call's working directory.
func (c Case) anchorDir() string {
	if c.Tool == "Bash" {
		return c.CWD
	}
	return filepath.Dir(c.FilePath)
}

// Set is the declared adversarial fixture set: every Case Decide must
// resolve correctly for the gate to satisfy the worktree-isolation invariant.
func Set() []Case {
	return []Case{
		// -- write: file writes into the primary checkout, any extension --
		// (DESIGN.md #2: no source-extension filter, every path is in scope)
		{Name: "write-go-source-in-primary", Category: Write, Topology: Primary, Tool: "Write", FilePath: "/repo/main.go"},
		{Name: "edit-doc-in-primary", Category: Write, Topology: Primary, Tool: "Edit", FilePath: "/repo/README.md"},
		{Name: "write-config-in-primary", Category: Write, Topology: Primary, Tool: "Write", FilePath: "/repo/config.yaml"},
		{Name: "write-data-in-primary", Category: Write, Topology: Primary, Tool: "Write", FilePath: "/repo/data.json"},
		{Name: "write-dotenv-in-primary", Category: Write, Topology: Primary, Tool: "Write", FilePath: "/repo/.env"},

		// -- write: Bash commands the classifier resolves as writes, in the
		// primary checkout (DESIGN.md #1: Bash as a conservative over-approximation)
		{Name: "bash-git-commit-in-primary", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git commit -m fix"},
		{Name: "bash-git-add-in-primary", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git add -A"},
		{Name: "bash-rm-in-primary", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "rm notes.txt"},
		{Name: "bash-mv-in-primary", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "mv a.go b.go"},
		{Name: "bash-sed-inplace-in-primary", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "sed -i s/a/b/ file.go"},
		{Name: "bash-redirect-in-primary", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "echo hi > out.txt"},
		{Name: "bash-append-in-primary", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "echo hi >> out.txt"},
		{Name: "bash-npm-install-in-primary", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "npm install left-pad"},
		{Name: "bash-read-then-write-in-primary", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git status && rm -rf build"},
		{Name: "bash-find-delete-in-primary", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "find . -delete"},

		// -- read: no-op calls this gate never governs --
		{Name: "write-empty-file-path-is-noop", Category: Read, Topology: Primary, Tool: "Write", FilePath: ""},
		{Name: "bash-blank-command-is-noop", Category: Read, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "   "},

		// -- read: Bash commands the classifier resolves as reads, in the
		// primary checkout --
		{Name: "bash-git-status-in-primary", Category: Read, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git status"},
		{Name: "bash-git-diff-in-primary", Category: Read, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git diff"},
		{Name: "bash-git-log-in-primary", Category: Read, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git log --oneline"},
		{Name: "bash-ls-in-primary", Category: Read, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "ls -la"},
		{Name: "bash-cat-in-primary", Category: Read, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "cat README.md"},
		{Name: "bash-grep-in-primary", Category: Read, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "grep TODO -r ."},

		// -- read: already inside a worktree, regardless of command shape --
		{Name: "write-in-worktree", Category: Read, Topology: WorktreeEntry, Tool: "Write", FilePath: "/repo/wt/main.go"},
		{Name: "bash-write-verb-in-worktree", Category: Read, Topology: WorktreeEntry, Tool: "Bash", CWD: "/repo/wt", Command: "git commit -m x"},

		// -- read: confidently outside any repository --
		{Name: "write-outside-any-repo", Category: Read, Topology: NoRepo, Tool: "Write", FilePath: "/scratch/file.txt"},
		{Name: "bash-write-verb-outside-any-repo", Category: Read, Topology: NoRepo, Tool: "Bash", CWD: "/scratch", Command: "rm -rf build"},

		// -- read: a genuine read is allowed even when the .git entry's own
		// kind can't be classified -- decideBash only denies a Bash call
		// once the command itself fails to classify as a clean read.
		{Name: "bash-read-verb-with-indeterminate-git-entry", Category: Read, Topology: Indeterminate, Tool: "Bash", CWD: "/repo", Command: "git status"},

		// -- uncertain: the `.git` entry exists but its kind can't be
		// classified (DESIGN.md #3: uncertain resolves to deny, not ask) --
		{Name: "write-indeterminate-git-entry", Category: Uncertain, Topology: Indeterminate, Tool: "Write", FilePath: "/repo/a.go"},
		{Name: "bash-non-read-with-indeterminate-git-entry", Category: Uncertain, Topology: Indeterminate, Tool: "Bash", CWD: "/repo", Command: "some-unlisted-tool"},

		// -- uncertain: a filesystem error during the repo-root walk --
		{Name: "write-fs-error-during-walk", Category: Uncertain, Topology: FSErr, Tool: "Write", FilePath: "/repo/a.go"},
		{Name: "bash-fs-error-during-walk", Category: Uncertain, Topology: FSErr, Tool: "Bash", CWD: "/repo", Command: "git status"},

		// -- uncertain: repo topology is clear but the command's intent
		// isn't (piped into an unrecognized tool) --
		{Name: "bash-piped-into-unknown-tool-in-primary", Category: Uncertain, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git log | some-custom-formatter"},

		// -- uncertain: no working directory reported for a Bash call --
		{Name: "bash-no-cwd-reported", Category: Uncertain, Topology: NoRepo, Tool: "Bash", CWD: "", Command: "git status"},

		// -- read: the incumbent's non-mutating-tool no-op, re-expressed --
		{Name: "read-tool-is-noop", Category: Read, Topology: Primary, Tool: "Read", FilePath: "/repo/a.go"},

		// -- read: tracking-doc exemption -- a Write/Edit under the configured
		// project dir whose basename is in the delivery-agent-team tracking-doc
		// set is allowed even in a primary checkout, matching the incumbent
		// write-locus-gate.sh's own $PROJ carve-out --
		{Name: "write-tracking-doc-exempt-plan-json", Category: Read, Topology: Primary, Tool: "Write", ProjectDir: "/proj", FilePath: "/proj/.dat/some-effort/plan.json"},
		{Name: "edit-tracking-doc-exempt-plan-json", Category: Read, Topology: Primary, Tool: "Edit", ProjectDir: "/proj", FilePath: "/proj/.dat/some-effort/plan.json"},
		{Name: "write-tracking-doc-exempt-design-md", Category: Read, Topology: Primary, Tool: "Write", ProjectDir: "/proj", FilePath: "/proj/.dat/some-effort/design.md"},
		{Name: "write-tracking-doc-exempt-plan-md", Category: Read, Topology: Primary, Tool: "Write", ProjectDir: "/proj", FilePath: "/proj/.dat/some-effort/plan.md"},
		{Name: "write-tracking-doc-exempt-execution-json", Category: Read, Topology: Primary, Tool: "Write", ProjectDir: "/proj", FilePath: "/proj/.dat/some-effort/execution.json"},
		{Name: "write-tracking-doc-exempt-execution-md", Category: Read, Topology: Primary, Tool: "Write", ProjectDir: "/proj", FilePath: "/proj/.dat/some-effort/execution.md"},
		{Name: "write-tracking-doc-exempt-feedback-json", Category: Read, Topology: Primary, Tool: "Write", ProjectDir: "/proj", FilePath: "/proj/.dat/some-effort/feedback.json"},
		{Name: "write-tracking-doc-exempt-feedback-md", Category: Read, Topology: Primary, Tool: "Write", ProjectDir: "/proj", FilePath: "/proj/.dat/some-effort/feedback.md"},
		{Name: "write-tracking-doc-exempt-at-any-depth", Category: Read, Topology: Primary, Tool: "Write", ProjectDir: "/proj", FilePath: "/proj/a/b/c/design.md"},

		// -- write: tracking-doc exemption narrowing negatives -- each
		// precondition drops separately, so the exemption stays pinned to
		// its exact case rather than widening --
		{Name: "write-tracking-doc-no-project-dir-denied", Category: Write, Topology: Primary, Tool: "Write", ProjectDir: "", FilePath: "/repo/.dat/some-effort/plan.json"},
		{Name: "write-tracking-doc-outside-project-dir-denied", Category: Write, Topology: Primary, Tool: "Write", ProjectDir: "/proj", FilePath: "/otherrepo/.dat/some-effort/plan.json"},
		{Name: "write-tracking-doc-nonexempt-basename-md-denied", Category: Write, Topology: Primary, Tool: "Write", ProjectDir: "/proj", FilePath: "/proj/.dat/some-effort/notes.md"},
		{Name: "write-tracking-doc-nonexempt-basename-json-denied", Category: Write, Topology: Primary, Tool: "Write", ProjectDir: "/proj", FilePath: "/proj/.dat/some-effort/config.json"},

		// -- read: sanctioned-landing-merge override -- with DAT_MERGE_GATE=1,
		// a bare `git merge`/`git commit` from the primary checkout is
		// allowed, matching build-with-team's documented landing flow --
		{Name: "bash-merge-gate-allows-merge", Category: Read, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git merge --no-ff feat/example -m mergemsg", MergeGateEnabled: true},
		{Name: "bash-merge-gate-allows-commit", Category: Read, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git commit -m done", MergeGateEnabled: true},

		// -- write/uncertain: sanctioned-landing-merge override narrowing
		// negatives -- unset stays byte-identical to today, a non-covered
		// verb stays denied even with the override on, and the override is
		// scoped to a primary checkout only --
		{Name: "bash-merge-without-gate-var-denied", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git merge --no-ff feat/example -m mergemsg", MergeGateEnabled: false},
		{Name: "bash-merge-gate-does-not-cover-push", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git push origin main", MergeGateEnabled: true},
		{Name: "bash-merge-gate-scoped-to-primary-denied", Category: Uncertain, Topology: Indeterminate, Tool: "Bash", CWD: "/repo", Command: "git merge --no-ff feat/example -m mergemsg", MergeGateEnabled: true},

		// -- write/uncertain: the override is unlaunderable -- a non-covered
		// write verb riding alongside a covered one stays denied, one fixture
		// per laundering form (&&, ;, |, subshell, env-prefix) --
		{Name: "bash-merge-gate-laundered-chain-denied", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git merge --no-ff feat/example -m mergemsg && rm -rf build", MergeGateEnabled: true},
		{Name: "bash-merge-gate-laundered-semicolon-denied", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git commit -m x; rm -rf build", MergeGateEnabled: true},
		{Name: "bash-merge-gate-laundered-pipe-denied", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git commit -m x | rm -rf build", MergeGateEnabled: true},
		{Name: "bash-merge-gate-laundered-subshell-denied", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "(git merge --no-ff feat/example -m mergemsg && rm -rf build)", MergeGateEnabled: true},
		{Name: "bash-merge-gate-laundered-env-prefix-denied", Category: Uncertain, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "FOO=bar git commit -m x", MergeGateEnabled: true},

		// -- write: the override is unlaunderable through write-carrying shell
		// metacharacters that live inside the single covered piece -- a
		// redirect, command/variable substitution, or backgrounding operator
		// must not ride along past the verb match --
		{Name: "bash-merge-gate-laundered-redirect-denied", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git commit -m x > pwned.txt", MergeGateEnabled: true},
		{Name: "bash-merge-gate-laundered-append-denied", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git commit -m x >> pwned.txt", MergeGateEnabled: true},
		{Name: "bash-merge-gate-laundered-cmdsubst-denied", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git commit -m \"$(rm -rf build)\"", MergeGateEnabled: true},
		{Name: "bash-merge-gate-laundered-backtick-denied", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git commit -m `rm -rf build`", MergeGateEnabled: true},
		{Name: "bash-merge-gate-laundered-background-denied", Category: Write, Topology: Primary, Tool: "Bash", CWD: "/repo", Command: "git commit -m x & rm -rf build", MergeGateEnabled: true},
	}
}
