package plugincheck

import (
	"encoding/json"
	"os"
	"testing"
)

// TestHooksJSON_WorktreeGateIsFirstPreToolUseHook is the build-acceptance test for
// SC-FORCEDUSE's ordering requirement: the worktree gate must run before forced-use
// routing on every PreToolUse dispatch, so a call outside a git worktree is denied
// before forced-use ever considers redirecting it to a git-tools subcommand.
func TestHooksJSON_WorktreeGateIsFirstPreToolUseHook(t *testing.T) {
	raw, err := os.ReadFile("../../plugin/hooks/hooks.json")
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}

	var doc struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal hooks.json: %v", err)
	}

	groups := doc.Hooks.PreToolUse
	if len(groups) < 2 {
		t.Fatalf("PreToolUse has %d groups, want at least 2 (worktree-gate, forced-use)", len(groups))
	}

	firstCommands := groups[0].Hooks
	if len(firstCommands) != 1 {
		t.Fatalf("first PreToolUse group has %d hooks, want 1", len(firstCommands))
	}
	if got := firstCommands[0].Command; got != `"${CLAUDE_PLUGIN_ROOT}/hooks/pretooluse-worktree-gate.sh"` {
		t.Errorf("first PreToolUse hook command = %q, want the worktree-gate wrapper", got)
	}

	sawForcedUseBeforeGate := false
	sawGate := false
	for _, g := range groups {
		for _, h := range g.Hooks {
			switch h.Command {
			case `"${CLAUDE_PLUGIN_ROOT}/hooks/pretooluse-worktree-gate.sh"`:
				sawGate = true
			case `"${CLAUDE_PLUGIN_ROOT}/hooks/pretooluse-forced-use.sh"`:
				if !sawGate {
					sawForcedUseBeforeGate = true
				}
			}
		}
	}
	if sawForcedUseBeforeGate {
		t.Error("forced-use hook appears before the worktree gate in PreToolUse ordering")
	}
	if !sawGate {
		t.Error("worktree-gate hook not found anywhere in PreToolUse")
	}
}
