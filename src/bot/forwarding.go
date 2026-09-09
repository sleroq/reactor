package bot

import (
	"math/rand"
	"time"

	"github.com/gotd/td/tg"
	"github.com/sleroq/reactor/src/db"
)

func (b *Bot) ForwardMessages(source db.Chat, destination tg.InputPeerClass, messages []db.Message, noQuote bool) error {
	generator := rand.New(rand.NewSource(time.Now().UnixNano()))

	msgIDs := make([]int, 0, len(messages))
	randomIDs := make([]int64, 0, len(messages))
	for _, msg := range messages {
		msgIDs = append(msgIDs, msg.ID)
		randomIDs = append(randomIDs, generator.Int63())
	}

	_, err := b.api.MessagesForwardMessages(b.ctx, &tg.MessagesForwardMessagesRequest{
		Flags:             0,
		Silent:            false,
		Background:        false,
		WithMyScore:       false,
		DropAuthor:        noQuote,
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
	})
	return err
}
