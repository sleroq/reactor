package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/go-faster/errors"
	"github.com/gotd/contrib/pebble"
	"github.com/gotd/contrib/storage"
	"github.com/gotd/td/tg"
	"github.com/sleroq/reactor/src/db"
	"github.com/sleroq/reactor/src/monitor"
	"go.uber.org/zap"
)

type HandlerContext struct {
	ctx     context.Context
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
	ctx      context.Context
	u        tg.MessageClass
	peerDB   *pebble.PeerStorage
	watcher  *monitor.Monitor
	bot      commandBot
	username string
}

// commandBot is the subset of Telegram operations needed by command handlers.
type commandBot interface {
	ReplyHelp(tg.InputPeerClass, int) error
	ReplyRating(tg.InputPeerClass, int, int, int) error
	ReplyToPeer(tg.InputPeerClass, int, string) error
}

func CommandMessageHandler(req CommandHandlerContext, options Options, logger *zap.SugaredLogger) (err error) {
	msg, ok := req.u.(*tg.Message)
	if !ok {
		return nil
	}

	command := commandName(msg.Message, req.username)
	if command == "" {
		return nil
	}
	logger.Infow("Handling command", "command", command, "text", msg.Message, "peer", fmt.Sprintf("%T", msg.GetPeerID()))

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

	switch command {
	case "r":
		return ratingCmd(req, msg, p.Channel, logger)
	case "help":
		return req.bot.ReplyHelp(&tg.InputPeerChannel{
			ChannelID:  p.Channel.ID,
			AccessHash: p.Channel.AccessHash,
		}, msg.ID)
	}

	return nil
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
		logger.Debugw("Ignoring rating command replying to a story", "story_id", v.StoryID)
		return nil
	default:
		return fmt.Errorf("unexpected reply type: %T", v)
	}

	if reply.ReplyToMsgID != 0 {
		rating, threshold, ratingErr := req.watcher.MessageRating(channel.ID, reply.ReplyToMsgID)
		if ratingErr != nil {
			if errors.Is(ratingErr, sql.ErrNoRows) {
				return req.bot.ReplyToPeer(&tg.InputPeerChannel{
					ChannelID:  channel.ID,
					AccessHash: channel.AccessHash,
				}, msg.ID, "404")
			}
			return errors.Wrap(ratingErr, "getting message rating")
		}

		replyErr := req.bot.ReplyRating(&tg.InputPeerChannel{
			ChannelID:  channel.ID,
			AccessHash: channel.AccessHash,
		}, msg.ID, rating, threshold)
		if replyErr != nil {
			return errors.Wrap(replyErr, "replying with message rating")
		}
	}

	return nil
}

func commandName(text, username string) string {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return ""
	}

	command := strings.ToLower(parts[0])
	for _, name := range []string{"r", "help"} {
		if command == "/"+name || username != "" && command == "/"+name+"@"+strings.ToLower(username) {
			return name
		}
	}

	return ""
}
