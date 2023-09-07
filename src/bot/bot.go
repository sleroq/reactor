package bot

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/sleroq/reactor/src/db"
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

func (b Bot) ForwardMessages(source db.Chat, destination tg.InputPeerClass, messages []db.Message, NoQuote bool) error {
	rSource := rand.NewSource(time.Now().UnixNano())
	generator := rand.New(rSource)

	var msgIDs []int
	var randomIDs []int64
	for _, msg := range messages {
		msgIDs = append(msgIDs, msg.ID)
		randomIDs = append(randomIDs, generator.Int63())
	}

	_, err := b.api.MessagesForwardMessages(
		b.ctx,
		&tg.MessagesForwardMessagesRequest{
			Flags:             0,
			Silent:            false,
			Background:        false,
			WithMyScore:       false,
			DropAuthor:        NoQuote,
			DropMediaCaptions: false,
			Noforwards:        false,
			FromPeer: &tg.InputPeerChannel{
				ChannelID:  source.ID,
				AccessHash: source.AccessHash,
			},
			ID:           msgIDs,
			RandomID:     randomIDs,
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
	fmt.Println("Requesting reactions list for message with id", msg.ID)
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
		if tgerr.IsCode(err, 400) {
			// Probably no reactions on this message yet
			return &tg.MessagesMessageReactionsList{}, nil
		}
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

func (b Bot) GetMessageText(chat tg.InputChannel, msgID int) (string, error) {
	// TODO: Maybe rewrite this part and make requests for multiple messages at once
	messages, err := b.api.ChannelsGetMessages(
		b.ctx,
		&tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{
				ChannelID:  chat.ChannelID,
				AccessHash: chat.AccessHash,
			},
			ID: []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}},
		},
	)
	if err != nil {
		return "", errors.Wrap(err, "getting message from telegram")
	}

	var messageClass tg.MessageClass
	switch v := messages.(type) {
	case *tg.MessagesChannelMessages: // messages.channelMessages#c776ba4e
		messageClass = v.Messages[0]
	default:
		return "", fmt.Errorf("unexpected messages type: %v %T", v, v)
	}

	var msg string
	switch v := messageClass.(type) {
	case *tg.Message: // message#38116ee0
		msg = v.Message
	default:
		return "", fmt.Errorf("unexpected message type: %v %T", v, v)
	}

	return msg, nil
}

func (b Bot) GetHistory(chatId int64, accessHash int64, limit int, offsetId int) ([]tg.MessageClass, error) {
	messages, err := b.api.MessagesGetHistory(
		b.ctx,
		&tg.MessagesGetHistoryRequest{
			Peer: &tg.InputPeerChannel{
				ChannelID:  chatId,
				AccessHash: accessHash,
			},
			Limit:      limit,
			OffsetID:   offsetId,
			OffsetDate: 0,
			MinID:      0,
			MaxID:      0,
			Hash:       0,
		},
	)
	if err != nil {
		return nil, errors.Wrap(err, "getting messages from telegram")
	}

	var messageClasses []tg.MessageClass
	switch v := messages.(type) {
	case *tg.MessagesMessages: // messages.messages#8c718e87
		messageClasses = v.Messages
	case *tg.MessagesChannelMessages: // messages.channelMessages#c776ba4e
		messageClasses = v.Messages
	default:
		return nil, fmt.Errorf("unexpected messages type: %v %T", v, v)
	}

	return messageClasses, nil
}
