package main

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-faster/errors"
	"github.com/gotd/contrib/pebble"
	"github.com/gotd/contrib/storage"
	"github.com/gotd/td/tg"
	botWrapper "github.com/sleroq/reactor/src/bot"
	"github.com/sleroq/reactor/src/db"
	"github.com/sleroq/reactor/src/helpers"
	"github.com/sleroq/reactor/src/monitor"
	"go.uber.org/zap"
)

type HandlerContext struct {
	ctx     context.Context
	e       tg.Entities
	u       *tg.UpdateNewChannelMessage
	peerDB  *pebble.PeerStorage
	botDB   *sql.DB
	watcher *monitor.Monitor
}

func ChannelMessageHandler(req HandlerContext, options Options, logger *zap.SugaredLogger) (err error) {
	msg, ok := req.u.Message.(*tg.Message)
	if !ok {
		return nil
	}

	// Use PeerID to find peer because *Short updates does not contain any entities, so it necessary to
	// store some entities.
	//
	// Storage can be filled using PeerCollector (i.e. fetching all dialogs first).
	p, err := storage.FindPeer(req.ctx, req.peerDB, msg.GetPeerID())
	if err != nil {
		return errors.Wrap(err, "finding peer")
	}

	// Skip if peer is not a channel.
	if p.Channel == nil {
		return nil
	}

	//fmt.Println(msg.Message, p.Channel.ID, p.Channel.AccessHash)
	//fmt.Println(helpers.FormatObject(msg))

	if !monitorsChannel(options.ChatsToMonitor, p.Channel.ID) {
		return nil
	}

	err = db.SaveChat(p.Channel, req.botDB)
	if err != nil {
		return errors.Wrap(err, "saving chat")
	}

	_, err = db.SaveMessage(msg, p.Channel.ID, req.botDB)
	if err != nil {
		fmt.Println(err)
		return errors.Wrap(err, "saving message")
	}

	return nil
}

type CommandHandlerContext struct {
	ctx     context.Context
	e       tg.Entities
	u       tg.MessageClass
	peerDB  *pebble.PeerStorage
	watcher *monitor.Monitor
	bot     *botWrapper.Bot
}

func CommandMessageHandler(req CommandHandlerContext, options Options, logger *zap.SugaredLogger) (err error) {
	msg, ok := req.u.(*tg.Message)
	if !ok {
		return nil
	}

	if !isRatingCommand(msg.Message) {
		return nil
	}

	p, err := storage.FindPeer(req.ctx, req.peerDB, msg.GetPeerID())
	if err != nil {
		return errors.Wrap(err, "finding peer")
	}

	if p.Channel == nil {
		return nil
	}

	if !monitorsChannel(options.ChatsToMonitor, p.Channel.ID) {
		return nil
	}

	return ratingCmd(req, msg, p.Channel, logger)
}

func monitorsChannel(chats []tg.InputPeerChannel, channelID int64) bool {
	for _, chat := range chats {
		if chat.ChannelID == channelID {
			return true
		}
	}

	return false
}

func ratingCmd(req CommandHandlerContext, msg *tg.Message, channel *tg.Channel, logger *zap.SugaredLogger) (err error) {
	if msg.ReplyTo == nil {
		return nil
	}

	var reply *tg.MessageReplyHeader
	switch v := msg.ReplyTo.(type) {
	case *tg.MessageReplyHeader: // messageReplyHeader#a6d57763
		reply = v
	case *tg.MessageReplyStoryHeader:
		logger.Debug("story reply, ignoring %s", helpers.FormatObject(msg))
		return fmt.Errorf("unexpected reply type: %T", v)
	default:
		return fmt.Errorf("unexpected reply type: %T", v)
	}

	if reply.ReplyToMsgID != 0 {
		rating, ratingErr := req.watcher.MessageRating(channel, reply.ReplyToMsgID)
		if ratingErr != nil {
			if errors.Is(ratingErr, sql.ErrNoRows) {
				return req.bot.ReplyToPeer(&tg.InputPeerChannel{
					ChannelID:  channel.ID,
					AccessHash: channel.AccessHash,
				}, msg.ID, "404")
			}
			return errors.Wrap(ratingErr, "getting message rating")
		}

		replyErr := req.bot.ReplyToPeer(&tg.InputPeerChannel{
			ChannelID:  channel.ID,
			AccessHash: channel.AccessHash,
		}, msg.ID, strconv.Itoa(rating))
		if replyErr != nil {
			return errors.Wrap(replyErr, "replying with message rating")
		}
	}

	return nil
}

func isRatingCommand(text string) bool {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return false
	}

	command := strings.ToLower(parts[0])
	command = strings.SplitN(command, "@", 2)[0]

	return command == "/r"
}
