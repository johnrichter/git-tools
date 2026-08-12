// Package gitexec spawns git subcommands rooted at a chosen directory and
// reports what each one did without interpreting its meaning.
//
// Inputs: a context, a working directory, and the subcommand's arguments.
// Outputs: git's raw result (exit code, captured stdout and stderr) via
// RunGit, or a decoded answer via the yes/no helpers built on it.
//
// Invariants:
//   - Only a failure to spawn git at all is a Go error; a non-zero exit is
//     git's ordinary way of answering a question, so RunGit returns it as a
//     Result and each caller owns the exit-code semantics of the subcommand it
//     asked for.
//   - Nothing here mutates process-global state or reaches the network beyond
//     what the requested subcommand does itself.
package gitexec

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/git"
	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// RunGit runs a git subcommand rooted at dir and returns its result for the
// caller to interpret. Only a spawn failure becomes a Go error here — a
// non-zero exit is git's ordinary way of answering a yes/no or reporting a
// real failure, and each caller knows which exit codes mean what for the
// subcommand it ran.
func RunGit(ctx context.Context, dir string, args ...string) (*sysops.Result, error) {
	res, err := sysops.Run(ctx, "git", args, sysops.Options{Dir: dir})
	if err != nil {
		return nil, fmt.Errorf("exec git %s: %w", strings.Join(args, " "), err)
	}
	return res, nil
}

// TreeDirty reports whether the working tree in dir has tracked modifications
// or staged changes relative to HEAD. `git diff` never considers untracked or
// ignored paths, so neither counts as dirtiness here.
func TreeDirty(ctx context.Context, dir string) (bool, error) {
	args := []string{"diff", "--quiet", "HEAD"}
	res, err := RunGit(ctx, dir, args...)
	if err != nil {
		return false, err
	}
	switch res.ExitCode {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
}

// CurrentBranch returns the short name of the branch HEAD is on in dir, or ""
// for a detached HEAD.
func CurrentBranch(ctx context.Context, dir string) (string, error) {
	res, err := RunGit(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", nil
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// RefExists reports whether refs/<namespace>/<ref> (namespace "heads" or
// "tags") exists locally in dir.
func RefExists(ctx context.Context, dir, namespace, ref string) (bool, error) {
	args := []string{"show-ref", "--verify", "--quiet", "refs/" + namespace + "/" + ref}
	res, err := RunGit(ctx, dir, args...)
	if err != nil {
		return false, err
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
}
