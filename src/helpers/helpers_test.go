package helpers

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestReactionPositivityRollingOnTheFloorLaughing(t *testing.T) {
	got, err := ReactionPositivity("🤣")
	if err != nil {
		t.Fatalf("ReactionPositivity() error = %v", err)
	}
	if got != 9 {
		t.Fatalf("ReactionPositivity() = %d, want 9", got)
	}
}

func TestAsReactionsCustomEmojiUsesBaseEmoji(t *testing.T) {
	reactions, err := AsReactions([]tg.MessagePeerReaction{{
		PeerID:   &tg.PeerUser{UserID: 1},
		Reaction: &tg.ReactionCustomEmoji{DocumentID: 42},
	}}, map[int64]string{42: "👎"}, 2, 3)
	if err != nil {
		t.Fatalf("AsReactions() error = %v", err)
	}
	if got := reactions[0].Emoticon; got != "👎" {
		t.Fatalf("AsReactions() emoticon = %q, want %q", got, "👎")
	}
}
