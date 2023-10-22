package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/go-faster/errors"
	"github.com/gotd/contrib/pebble"
	"github.com/gotd/contrib/storage"
	"github.com/gotd/td/tg"
	"github.com/sleroq/reactor/src/db"
	"github.com/sleroq/reactor/src/helpers"
	"github.com/sleroq/reactor/src/monitor"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"
	"strings"
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

	allowed := slices.ContainsFunc(options.ChatsToMonitor, func(ch tg.InputPeerChannel) bool {
		if p.Channel.ID == ch.ChannelID {
			return true
		}
		return false
	})
	if !allowed {
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

	if strings.HasPrefix(msg.Message, "/r") {
		err := ratingCmd(req, msg, p, logger)
		if err != nil {
			return errors.Wrap(err, "handling rating command")
		}
	}

	return nil
}

func ratingCmd(req HandlerContext, msg *tg.Message, p storage.Peer, logger *zap.SugaredLogger) (err error) {
	if msg.ReplyTo == nil {
		return nil
	}

	var reply *tg.MessageReplyHeader
	switch v := msg.ReplyTo.(type) {
	case *tg.MessageReplyHeader: // messageReplyHeader#a6d57763
		reply = v
		break
	case *tg.MessageReplyStoryHeader:
		logger.Debug("story reply, ignoring %s", helpers.FormatObject(msg))
		return fmt.Errorf("unexpected reply type: %T", reply)
	default:
		return fmt.Errorf("unexpected reply type: %T", reply)
	}

	if reply.ReplyToMsgID != 0 {
		err = req.watcher.ReplyMessageRating(req.e, req.u, reply.ReplyToMsgID, p.Channel)
		if err != nil {
			return errors.Wrap(err, "replying with message rating")
		}
	}
	return err
}
