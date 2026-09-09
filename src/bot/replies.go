package bot

import (
	_ "embed"
	"fmt"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
)

//go:embed help.ogg
var helpVoice []byte

func (b *Bot) Reply(e tg.Entities, u *tg.UpdateNewChannelMessage, text string) error {
	sender := message.NewSender(b.api)
	_, err := sender.Reply(e, u).Text(b.ctx, text)
	if err != nil {
		return errors.Wrap(err, "sending reply")
	}
	return nil
}

func (b *Bot) ReplyToPeer(peer tg.InputPeerClass, replyID int, text string) error {
	sender := message.NewSender(b.api)
	_, err := sender.To(peer).CloneBuilder().Reply(replyID).Text(b.ctx, text)
	if err != nil {
		return errors.Wrap(err, "sending reply")
	}
	return nil
}

func (b *Bot) ReplyRating(peer tg.InputPeerClass, replyID, current, threshold int) error {
	sender := message.NewSender(b.api)
	_, err := sender.To(peer).CloneBuilder().Reply(replyID).StyledText(
		b.ctx,
		styling.Bold(fmt.Sprintf("%d", current)),
		styling.Plain(fmt.Sprintf(" / %d", threshold)),
	)
	if err != nil {
		return errors.Wrap(err, "sending rating reply")
	}
	return nil
}

func (b *Bot) ReplyHelp(peer tg.InputPeerClass, replyID int) error {
	sender := message.NewSender(b.api)
	_, err := sender.To(peer).CloneBuilder().Reply(replyID).Upload(message.FromBytes("help.ogg", helpVoice)).Voice(b.ctx)
	if err != nil {
		return errors.Wrap(err, "sending help voice message")
	}
	return nil
}
