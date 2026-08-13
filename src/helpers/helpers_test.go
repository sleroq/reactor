package helpers

import "testing"

func TestReactionPositivityRollingOnTheFloorLaughing(t *testing.T) {
	got, err := ReactionPositivity("🤣")
	if err != nil {
		t.Fatalf("ReactionPositivity() error = %v", err)
	}
	if got != 9 {
		t.Fatalf("ReactionPositivity() = %d, want 9", got)
	}
}
