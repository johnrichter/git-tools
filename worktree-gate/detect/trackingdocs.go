package detect

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed trackingdocs.json
var trackingDocsJSON []byte

// TrackingDocs is the basename set decideFileWrite exempts from the
// primary-checkout deny when the target also lives under the configured
// project directory (delivery-agent-team's tracking-doc set).
type TrackingDocs struct {
	Basenames []string `json:"basenames"`
}

// DefaultTrackingDocs parses the embedded tracking-doc basename set. A
// non-nil error means the shipped artifact itself is malformed or empty --
// a packaging defect, not a signal about any tool call. The gate treats it
// fail-closed: a Write/Edit the exemption could have covered -- a target
// under the configured project dir -- denies on the defect rather than risk
// an unisolated write. A target outside that scope is unaffected, since the
// exemption could never have applied to it.
func DefaultTrackingDocs() (TrackingDocs, error) {
	var td TrackingDocs
	if err := json.Unmarshal(trackingDocsJSON, &td); err != nil {
		return TrackingDocs{}, fmt.Errorf("worktree-gate: embedded trackingdocs.json is corrupt: %w", err)
	}
	if len(td.Basenames) == 0 {
		return TrackingDocs{}, fmt.Errorf("worktree-gate: embedded trackingdocs.json carries no basenames")
	}
	return td, nil
}

// has reports whether basename is in the tracking-doc set.
func (td TrackingDocs) has(basename string) bool {
	for _, b := range td.Basenames {
		if b == basename {
			return true
		}
	}
	return false
}
