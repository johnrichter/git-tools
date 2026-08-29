// Package result translates git-library errors and outcomes into the
// clikit.Result shapes git-tools' commands emit.
package result

import (
	"errors"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/git"
)

// sanitize collapses msg to the single control-character-free, bounded line
// clikit.NewError requires: a git error's text embeds free-form,
// caller-supplied file paths (a large conflict can list enough to exceed the
// length bound), which must not turn a well-classified conflict into a
// build failure and a misclassified internal result.
func sanitize(msg string) string {
	folded := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, msg)
	joined := strings.Join(strings.Fields(folded), " ")
	const max = 4096
	if len(joined) > max {
		joined = joined[:max]
	}
	return joined
}

// ConflictDiagnostic builds the conflict-class diagnostic for a git-library
// error that is one of the two specifically classified failure modes: a
// stale compare-and-swap ref move, or an aborted merge/rebase conflict. ok
// is false for any other error, leaving the caller to classify it itself
// (typically as an internal failure).
func ConflictDiagnostic(err error) (diag clikit.Diagnostic, ok bool, buildErr error) {
	var stale *git.StaleRefError
	if errors.As(err, &stale) {
		diag, buildErr = clikit.NewError(
			"conflict.git.stale_ref",
			sanitize(err.Error()),
			clikit.Manual("re-read the ref's current value and retry with the updated expected commit"),
			map[string]any{"ref": stale.Ref, "expected_old": stale.ExpectedOld, "actual_old": stale.ActualOld},
		)
		return diag, true, buildErr
	}

	var conflict *git.ConflictError
	if errors.As(err, &conflict) {
		diag, buildErr = clikit.NewError(
			"conflict.git."+conflict.Op+"_conflict",
			sanitize(err.Error()),
			clikit.Manual("resolve the conflicting file(s) manually, or choose a different base/upstream"),
			map[string]any{"op": conflict.Op, "files": conflict.Files},
		)
		return diag, true, buildErr
	}

	return clikit.Diagnostic{}, false, nil
}

// RewriteOutcomeData builds the `data` map for a clikit.Result describing a
// history-rewriting operation (resign, rebase, branch delete).
func RewriteOutcomeData(o *git.RewriteOutcome) map[string]any {
	data := map[string]any{
		"ref":        o.Ref,
		"old_head":   o.OldHead,
		"backup_ref": o.BackupRef,
		"dry_run":    o.DryRun,
	}
	if o.NewHead != "" {
		data["new_head"] = o.NewHead
	}
	if len(o.PushCmd) > 0 {
		data["push_cmd"] = o.PushCmd
	}
	return data
}
