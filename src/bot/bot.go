package bot

import (
	"context"

	"github.com/gotd/td/tg"
)

// Bot is the Telegram adapter used by the monitor and command handlers.
type Bot struct {
	ctx context.Context
	api *tg.Client

	emojiCache *customEmojiCache
}

func New(ctx context.Context, api *tg.Client) *Bot {
	return &Bot{
		ctx:        ctx,
		api:        api,
		emojiCache: newCustomEmojiCache(),
	}
}
