// Package hooks installs git-tools' scans as a tracked, versionable git
// hook: a script under the repository's hooks directory plus
// core.hooksPath, rather than a file dropped directly into the untracked
// .git/hooks directory.
package hooks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// ErrHookExists is the sentinel wrapped into Install's error when the target
// hook script already exists and Force is not set — a state a caller
// classifies as a conflict, not an infrastructure failure.
var ErrHookExists = errors.New("hooks: hook script already exists")

// scriptTemplate is the installed hook's entire body: it execs straight into
// git-tools' own combined scan, so the hook carries no logic of its own to
// drift from the CLI it delegates to. --staged narrows only the raw-binary
// check to the staged diff; secrets and privacy scan the full tracked tree
// on every invocation, by design (see "scan all"'s --staged flag help).
const scriptTemplate = `#!/bin/sh
# Installed by "git-tools hooks install". Re-run that command to change
# settings instead of editing this file by hand.
exec %s scan all --staged --privacy-tier %s%s
`

// RenderScript builds the hook script body for binary (the git-tools
// executable name or path the hook should invoke), tier (the baked-in
// --privacy-tier) and strict (whether privacy warnings escalate to
// failures). It is a pure function of its inputs.
func RenderScript(binary, tier string, strict bool) string {
	strictFlag := ""
	if strict {
		strictFlag = " --strict"
	}
	return fmt.Sprintf(scriptTemplate, shellQuote(binary), shellQuote(tier), strictFlag)
}

// shellQuote wraps s in single quotes for safe use as one POSIX sh word,
// escaping any single quote it contains.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// InstallOptions configures Install.
type InstallOptions struct {
	RepoDir     string // the git working tree to install into
	HooksDir    string // hook script directory, relative to RepoDir (e.g. ".githooks")
	HookName    string // git hook name, e.g. "pre-commit"
	Binary      string // git-tools executable the installed hook invokes
	PrivacyTier string
	Strict      bool
	Force       bool // overwrite an existing hook script at the target path
	DryRun      bool // report the plan without writing anything
}

// InstallResult reports what Install did, or (for a dry run) would do.
type InstallResult struct {
	Path        string
	HooksDir    string
	Overwritten bool
	DryRun      bool
}

// Install writes the rendered hook script to RepoDir/HooksDir/HookName and
// points the repository's core.hooksPath at HooksDir, so the hook survives a
// fresh clone instead of living only in the untracked .git/hooks directory.
func Install(ctx context.Context, opts InstallOptions) (*InstallResult, error) {
	hooksAbsDir := filepath.Join(opts.RepoDir, opts.HooksDir)
	path := filepath.Join(hooksAbsDir, opts.HookName)

	_, statErr := os.Stat(path)
	exists := statErr == nil
	if exists && !opts.Force {
		return nil, fmt.Errorf("%s: %w (pass --force to overwrite)", path, ErrHookExists)
	}

	result := &InstallResult{Path: path, HooksDir: opts.HooksDir, Overwritten: exists, DryRun: opts.DryRun}
	if opts.DryRun {
		return result, nil
	}

	if err := os.MkdirAll(hooksAbsDir, 0o755); err != nil {
		return nil, fmt.Errorf("hooks: create %s: %w", hooksAbsDir, err)
	}
	script := RenderScript(opts.Binary, opts.PrivacyTier, opts.Strict)
	if err := fsx.WriteAtomic(path, []byte(script), 0o755); err != nil {
		return nil, err
	}

	res, err := sysops.Run(ctx, "git", []string{"config", "core.hooksPath", opts.HooksDir}, sysops.Options{Dir: opts.RepoDir})
	if err != nil {
		return nil, fmt.Errorf("hooks: set core.hooksPath: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("hooks: git config core.hooksPath %s: exit %d: %s", opts.HooksDir, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return result, nil
}
