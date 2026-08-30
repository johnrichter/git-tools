// Package commitmsg checks a commit message git-tools is about to mint
// against whatever commit-msg hook the repository already has configured,
// before the verb that would mint it runs. It names no format rule of its
// own: a repository with no commit-msg hook configured (no core.hooksPath,
// no .git/hooks/commit-msg) gets no check at all, and one with a hook gets
// exactly that hook's own verdict, run explicitly rather than left to fire
// only as a side effect of whichever porcelain git command happens to mint
// the commit.
package commitmsg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/git"
	"github.com/johnrichter/claude-shared-tooling/go/sysops"

	"github.com/johnrichter/git-tools/internal/gitexec"
)

// Refusal is the commit-message gate's decision that the repository's own
// configured commit-msg hook rejects the message a verb is about to mint. It
// implements error so a plain-error caller can return it directly, and it
// also carries the fields a clikit caller needs to emit it as a diagnostic.
type Refusal struct {
	code    string
	message string
	advice  clikit.Triage
}

// Error returns the refusal's raw, unsanitized message, satisfying the error
// interface so a Refusal can be returned wherever a plain error is expected.
func (r *Refusal) Error() string { return r.message }

// Code returns the refusal's diagnostic code.
func (r *Refusal) Code() string { return r.code }

// Message returns the refusal's raw, unsanitized human-readable message. The
// caller that emits it as a diagnostic is responsible for sanitizing it.
func (r *Refusal) Message() string { return r.message }

// Advice returns the triage guidance for recovering from the refusal.
func (r *Refusal) Advice() clikit.Triage { return r.advice }

// Check resolves dir's configured commit-msg hook -- core.hooksPath if set,
// else the default .git/hooks -- and, only when one exists there and is
// executable, runs it against message exactly as git itself would: the
// message in a scratch file, passed as the hook's sole argument. It returns
// a *Refusal when the hook exits non-zero, and (nil, nil) both when the hook
// accepts the message and when no hook is configured at all -- an absent
// hook is a no-op, never a fallback to some check of this package's own.
func Check(ctx context.Context, dir, message string) (*Refusal, error) {
	hook, ok, err := resolveHook(ctx, dir)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	tmp, err := os.CreateTemp("", "git-tools-commit-msg-*")
	if err != nil {
		return nil, fmt.Errorf("commitmsg: create scratch message file: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.WriteString(message)
	closeErr := tmp.Close()
	if writeErr != nil {
		return nil, fmt.Errorf("commitmsg: write scratch message file: %w", writeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("commitmsg: close scratch message file: %w", closeErr)
	}

	res, err := sysops.Run(ctx, hook, []string{tmp.Name()}, sysops.Options{Dir: dir})
	if err != nil {
		return nil, fmt.Errorf("commitmsg: run %s: %w", hook, err)
	}
	if res.ExitCode == 0 {
		return nil, nil
	}

	detail := strings.TrimSpace(string(res.Stderr))
	if detail == "" {
		detail = strings.TrimSpace(string(res.Stdout))
	}
	if detail == "" {
		detail = fmt.Sprintf("exit %d", res.ExitCode)
	}
	return &Refusal{
		code:    "precondition_unmet.git.commit_message_hook_rejected",
		message: fmt.Sprintf("the configured commit-msg hook (%s) rejected the commit message: %s", hook, detail),
		advice:  clikit.Manual("fix the commit message to satisfy the configured commit-msg hook and re-run; nothing was committed"),
	}, nil
}

// resolveHook reports the path to dir's configured commit-msg hook, and
// whether it exists there as an executable file. A missing or
// non-executable candidate is a plain "no hook configured" answer, never an
// error -- this package polices nothing about what a hook must look like,
// only whether one is there to delegate to.
func resolveHook(ctx context.Context, dir string) (string, bool, error) {
	hooksDir, err := hooksDirectory(ctx, dir)
	if err != nil {
		return "", false, err
	}
	candidate := filepath.Join(hooksDir, "commit-msg")
	info, statErr := os.Stat(candidate)
	if statErr != nil || info.IsDir() {
		return "", false, nil
	}
	if info.Mode()&0o111 == 0 {
		return "", false, nil
	}
	return candidate, true, nil
}

// hooksDirectory resolves the repository's effective hooks directory the
// same way git itself does: core.hooksPath (local, global, or system --
// read through git's own config resolution, not just the local file) when
// set, relative paths taken as relative to dir per git-config(1); otherwise
// the default hooks directory under the repository's own git directory.
func hooksDirectory(ctx context.Context, dir string) (string, error) {
	res, err := gitexec.RunGit(ctx, dir, "config", "--get", "core.hooksPath")
	if err != nil {
		return "", err
	}
	switch res.ExitCode {
	case 0:
		configured := strings.TrimSpace(string(res.Stdout))
		if filepath.IsAbs(configured) {
			return configured, nil
		}
		return filepath.Join(dir, configured), nil
	case 1:
		// Unset, not a failure: fall through to git's own default below.
	default:
		return "", &git.CommandError{Args: []string{"config", "--get", "core.hooksPath"}, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}

	res, err = gitexec.RunGit(ctx, dir, "rev-parse", "--path-format=absolute", "--git-path", "hooks")
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", &git.CommandError{Args: []string{"rev-parse", "--path-format=absolute", "--git-path", "hooks"}, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}
