package discordtext

import (
	"github.com/quackdiscord/bot/internal/quack/model"
	"testing"
)

// TestReversalSentencesDescribeResultingState remains accurate whether a guarded
// operation removed the punishment or confirmed that it was already absent.
func TestReversalSentencesDescribeResultingState(t *testing.T) {
	for action, want := range map[model.ActionType]string{model.ActionRemoveTimeout: "Member is no longer timed out.", model.ActionUnbanUser: "Member is no longer banned."} {
		if got := ActionSentence(action, model.ActionExecutionSucceeded); got != want {
			t.Fatalf("%s: %s", action, got)
		}
	}
}
