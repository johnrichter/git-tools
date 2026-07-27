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
// packaging defect, not a signal about any tool call. Callers must treat
// it as fail-open-and-loud, never let it drive a deny.
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
