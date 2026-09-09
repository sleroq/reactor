package bot

import (
	"fmt"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
)

func (b *Bot) GetMessageText(chat tg.InputChannel, msgID int) (string, error) {
	messages, err := b.api.ChannelsGetMessages(b.ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: chat.ChannelID, AccessHash: chat.AccessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}},
	})
	if err != nil {
		return "", errors.Wrap(err, "getting message from telegram")
	}

	channelMessages, ok := messages.(*tg.MessagesChannelMessages)
	if !ok {
		return "", fmt.Errorf("unexpected messages type: %T", messages)
	}
	if len(channelMessages.Messages) == 0 {
		return "", fmt.Errorf("message %d not found", msgID)
	}

	msg, ok := channelMessages.Messages[0].(*tg.Message)
	if !ok {
		return "", fmt.Errorf("unexpected message type: %T", channelMessages.Messages[0])
	}
	return msg.Message, nil
}

func (b *Bot) GetMessages(chatID, accessHash int64, messageIDs []int) ([]tg.MessageClass, error) {
	ids := make([]tg.InputMessageClass, 0, len(messageIDs))
	for _, messageID := range messageIDs {
		ids = append(ids, &tg.InputMessageID{ID: messageID})
	}

	messages, err := b.api.ChannelsGetMessages(b.ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: chatID, AccessHash: accessHash},
		ID:      ids,
	})
	if err != nil {
		return nil, errors.Wrap(err, "getting channel messages from telegram")
	}

	switch messages := messages.(type) {
	case *tg.MessagesMessages:
		return messages.Messages, nil
	case *tg.MessagesChannelMessages:
		return messages.Messages, nil
	default:
		return nil, fmt.Errorf("unexpected messages type: %T", messages)
	}
}
