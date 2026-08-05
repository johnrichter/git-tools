package detect

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed verbs.json
var verbsJSON []byte

// DefaultVerbs parses the classifier's embedded pattern set. A non-nil
// error means the shipped artifact itself is malformed or empty -- a
// packaging defect, not a signal about any tool call. The gate treats it
// fail-closed: the defect could be masking a real write, so a Bash call
// whose location is not already independently safe denies on it. Only a
// call resolved without the classifier -- confirmed inside a worktree --
// is allowed, with the defect surfaced as a loud diagnostic
// (Decision.Degraded), never as the reason for a verdict.
func DefaultVerbs() (Verbs, error) {
	var v Verbs
	if err := json.Unmarshal(verbsJSON, &v); err != nil {
		return Verbs{}, fmt.Errorf("worktree-gate: embedded verbs.json is corrupt: %w", err)
	}
	if len(v.ReadPrefixes) == 0 && len(v.WritePrefixes) == 0 && len(v.WriteContains) == 0 {
		return Verbs{}, fmt.Errorf("worktree-gate: embedded verbs.json carries no patterns")
	}
	return v, nil
}
