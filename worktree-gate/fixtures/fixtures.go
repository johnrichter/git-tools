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
}

// wantDeny is the invariant Category encodes: only Read fixtures may pass
// through the gate.
func (c Case) wantDeny() bool { return c.Category != Read }

func (c Case) toInput() detect.Input {
	return detect.Input{ToolName: c.Tool, CWD: c.CWD, FilePath: c.FilePath, Command: c.Command}
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
// resolve correctly for the gate to satisfy SC-WORKTREE.
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
	}
}
