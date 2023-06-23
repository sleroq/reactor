package bot

import (
	"context"
	"fmt"
	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/tg"
	"github.com/sleroq/memeq/src/db"
	"math/rand"
	"time"
)

// Part function splits slice on specified position
func Part[T any](slice []T, length int) (new []T, modified []T) {
	if length > len(slice) {
		length = len(slice)
	}
	return slice[:length], slice[length:]
}

type Bot struct {
	ctx context.Context
	api *tg.Client
}

func New(ctx context.Context, api *tg.Client) *Bot {
	return &Bot{
		ctx: ctx,
		api: api,
	}
}

func (b Bot) ForwardMessage(source db.Chat, destination tg.InputPeerClass, messageID int) error {
	rSource := rand.NewSource(time.Now().UnixNano())
	generator := rand.New(rSource)

	_, err := b.api.MessagesForwardMessages(
		b.ctx,
		&tg.MessagesForwardMessagesRequest{
			Flags:             0,
			Silent:            false,
			Background:        false,
			WithMyScore:       false,
			DropAuthor:        true,
			DropMediaCaptions: false,
			Noforwards:        false,
			FromPeer: &tg.InputPeerChannel{
				ChannelID:  source.ID,
				AccessHash: source.AccessHash,
			},
			ID:           []int{messageID},
			RandomID:     []int64{generator.Int63()},
			ToPeer:       destination,
			TopMsgID:     0,
			ScheduleDate: 0,
			SendAs:       nil,
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func (b Bot) GetReactions(chatId int64, accessHash int64, messages []int) ([]*tg.UpdateMessageReactions, error) {
	update, err := b.api.MessagesGetMessagesReactions(b.ctx, &tg.MessagesGetMessagesReactionsRequest{
		Peer: &tg.InputPeerChannel{
			ChannelID:  chatId,
			AccessHash: accessHash,
		},
		ID: messages,
	})
	if err != nil {
		return nil, errors.Wrap(err, "getting reactions update for message")
	}

	var reactionUpdates []*tg.UpdateMessageReactions
	switch v := update.(type) {
	case *tg.Updates:
		for _, reactionUpdate := range v.Updates {
			switch u := reactionUpdate.(type) {
			case *tg.UpdateMessageReactions:
				reactionUpdates = append(reactionUpdates, u)
			default:
				return nil, fmt.Errorf("unexpected update type: %s", u)
			}
		}
	default:
		return nil, fmt.Errorf("unexpected update type: %s", v)
	}

	return reactionUpdates, nil
}

func (b Bot) GetMessagesReactions(chat db.Chat, messages []db.Message) (
	reactions []*tg.UpdateMessageReactions,
	err error,
) {
	for len(messages) > 0 {
		var part []db.Message
		part, messages = Part(messages, 90)
		var messageIds []int
		for _, msg := range part {
			messageIds = append(messageIds, msg.ID)
		}

		fmt.Println("requesting reactions for", len(messageIds), "messages")
		someReactions, err := b.GetReactions(chat.ID, chat.AccessHash, messageIds)
		if err != nil {
			return nil, errors.Wrap(err, "getting reactions")
		}

		reactions = append(reactions, someReactions...)
	}

	return reactions, nil
}

func (b Bot) GetReactionsList(msg db.Message, accessHash int64) (*tg.MessagesMessageReactionsList, error) {
	fmt.Println("Requesting reactions list")
	reactionsList, err := b.api.MessagesGetMessageReactionsList(
		b.ctx,
		&tg.MessagesGetMessageReactionsListRequest{
			Flags: 0,
			Peer: &tg.InputPeerChannel{
				ChannelID:  msg.ChatID,
				AccessHash: accessHash,
			},
			ID:       msg.ID,
			Reaction: nil,
			Offset:   "",
			Limit:    100,
		})
	if err != nil {
		return nil, err
	}

	return reactionsList, nil
}

func (b Bot) Reply(e tg.Entities, u *tg.UpdateNewChannelMessage, text string) error {
	sender := message.NewSender(b.api)
	_, err := sender.Reply(e, u).Text(b.ctx, text)
	if err != nil {
		fmt.Println(err)
		return errors.Wrap(err, "sending reply")
	}

	return nil
}
