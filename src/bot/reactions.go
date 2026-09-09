package bot

import (
	"fmt"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/sleroq/reactor/src/db"
	"go.uber.org/zap"
)

func part[T any](slice []T, length int) ([]T, []T) {
	if length > len(slice) {
		length = len(slice)
	}
	return slice[:length], slice[length:]
}

func (b *Bot) getReactions(chatID, accessHash int64, messages []int) ([]*tg.UpdateMessageReactions, error) {
	update, err := b.api.MessagesGetMessagesReactions(b.ctx, &tg.MessagesGetMessagesReactionsRequest{
		Peer: &tg.InputPeerChannel{ChannelID: chatID, AccessHash: accessHash},
		ID:   messages,
	})
	if err != nil {
		return nil, errors.Wrap(err, "getting reactions update for message")
	}

	updates, ok := update.(*tg.Updates)
	if !ok {
		return nil, fmt.Errorf("unexpected update type: %T", update)
	}

	reactionUpdates := make([]*tg.UpdateMessageReactions, 0, len(updates.Updates))
	for _, update := range updates.Updates {
		reactionUpdate, ok := update.(*tg.UpdateMessageReactions)
		if !ok {
			return nil, fmt.Errorf("unexpected update type: %T", update)
		}
		reactionUpdates = append(reactionUpdates, reactionUpdate)
	}
	return reactionUpdates, nil
}

func (b *Bot) GetMessagesReactions(chat db.Chat, messages []db.Message, delay time.Duration, logger *zap.SugaredLogger) ([]*tg.UpdateMessageReactions, error) {
	var reactions []*tg.UpdateMessageReactions
	for len(messages) > 0 {
		var batch []db.Message
		batch, messages = part(messages, 90)
		messageIDs := make([]int, 0, len(batch))
		for _, msg := range batch {
			messageIDs = append(messageIDs, msg.ID)
		}

		logger.Debugln("requesting reactions for messages", len(messageIDs), "messages")
		someReactions, err := b.getReactions(chat.ID, chat.AccessHash, messageIDs)
		if err != nil {
			return nil, errors.Wrap(err, "getting reactions")
		}
		reactions = append(reactions, someReactions...)

		if len(messages) > 0 {
			logger.Debugw("waiting for next request", "delay", delay)
			select {
			case <-b.ctx.Done():
				return nil, b.ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return reactions, nil
}

// GetReactionsList returns at most 100 reactions for a message.
func (b *Bot) GetReactionsList(msg db.Message, accessHash int64) (*tg.MessagesMessageReactionsList, error) {
	reactionsList, err := b.api.MessagesGetMessageReactionsList(b.ctx, &tg.MessagesGetMessageReactionsListRequest{
		Flags: 0,
		Peer:  &tg.InputPeerChannel{ChannelID: msg.ChatID, AccessHash: accessHash},
		ID:    msg.ID, Reaction: nil, Offset: "", Limit: 100,
	})
	if err != nil {
		if tgerr.IsCode(err, 400) {
			return &tg.MessagesMessageReactionsList{}, nil
		}
		return nil, err
	}
	return reactionsList, nil
}
