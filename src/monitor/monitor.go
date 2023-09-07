package monitor

import (
	"database/sql"
	"fmt"
	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	"github.com/sleroq/reactor/src/bot"
	"golang.org/x/exp/slices"

	"regexp"
	"strings"
	"time"

	"github.com/sleroq/reactor/src/db"
	"github.com/sleroq/reactor/src/helpers"
)

type Thresholds struct {
	Text    int
	Photo   int
	Forward int
}
type Chats struct {
	Sources      []tg.InputPeerChannel
	Destinations []tg.InputPeerClass
}

type Options struct {
	Thresholds       Thresholds
	Chats            Chats
	NoQuoteWhitelist []int64
}

type Monitor struct {
	db      *sql.DB
	bot     *bot.Bot
	options Options
}

func New(options Options, db *sql.DB, bot *bot.Bot) *Monitor {
	return &Monitor{
		db,
		bot,
		options,
	}
}

// Start runs reactions & replies monitor
// delay - duration between each check
// ageLimit - duration in which messages will be monitored
func (m Monitor) Start(delay time.Duration, ageLimit time.Duration) error {
	go func() {
		if err := m.checkForMissedMessages(); err != nil {
			fmt.Println("error checking for missed messages:", err)
		}
	}()

	for range time.Tick(delay) {
		if err := m.checkForNewMessages(ageLimit); err != nil {
			return err
		}
	}
	return nil
}

func (m Monitor) checkForNewMessages(ageLimit time.Duration) error {
	startDate := time.Now().Add(-ageLimit)

	chats, err := db.GetOnlySavedChats(m.options.Chats.Sources, m.db)
	if err != nil {
		return errors.Wrap(err, "getting saved chats from database")
	}

	for _, chat := range chats {
		messages, err := db.GetMessagesAfter(m.db, chat.ID, startDate)
		if err != nil {
			return errors.Wrap(err, "getting messages from database")
		}

		err = m.checkMessages(chat, messages)
		if err != nil {
			return errors.Wrap(err, "checking messages")
		}

		// Sleep for 3 seconds to avoid hitting the rate limit
		time.Sleep(3 * time.Second)
	}

	return nil
}

func (m Monitor) checkMessages(chat db.Chat, messages []db.Message) error {
	reactionUpdates, err := m.bot.GetMessagesReactions(chat, messages)
	if err != nil {
		return errors.Wrap(err, "getting reactions for all messages")
	}

	reactionsGroup := make(map[int]tg.MessageReactions)
	messagesGroup := make(map[int]db.Message)
	for _, msg := range messages {
		messagesGroup[msg.ID] = msg
	}

	for _, update := range reactionUpdates {
		reactionsGroup[update.MsgID] = update.Reactions
	}

	// Update reactions per-message
	for messageId, msgReactions := range reactionsGroup {
		msg := messagesGroup[messageId]

		reactions, err := m.syncReactions(msgReactions, msg, chat.AccessHash)
		if err != nil {
			return errors.Wrap(err, "syncing reactions")
		}

		// Ignore already forwarded messages
		if msg.Forwarded {
			continue
		}

		totalRating, err := m.rateMessage(reactions, msg)
		if err != nil {
			return errors.Wrap(err, "rating message")
		}

		threshold := m.options.Thresholds.Forward
		if msg.FwdFromChannel == 0 &&
			msg.FwdFromUser == 0 {
			threshold = m.options.Thresholds.Photo
		}
		if !msg.WithPhoto {
			threshold = m.options.Thresholds.Text
		}

		if totalRating > threshold {
			// Checking to see if message was edited
			msg, err := m.UpdateMessage(tg.InputChannel{
				ChannelID:  chat.ID,
				AccessHash: chat.AccessHash,
			}, msg)
			finalRating, err := m.rateMessage(reactions, msg)
			if err != nil {
				return errors.Wrap(err, "rating message")
			}
			if finalRating <= threshold {
				return nil
			}

			fmt.Println(
				"forwarding msg", messageId,
				"with", totalRating, "rating",
			)

			noQuote := true
			if slices.Contains(m.options.NoQuoteWhitelist, msg.FwdFromChannel) {
				noQuote = false
			}
			if slices.Contains(m.options.NoQuoteWhitelist, msg.FwdFromUser) {
				noQuote = false
			}
			if slices.Contains(m.options.NoQuoteWhitelist, msg.UserID) {
				noQuote = false
			}

			messages := []db.Message{msg}
			if msg.GroupedID != 0 {
				messages, err = db.GetMessagesGroup(m.db, msg.GroupedID)
			}

			for _, destination := range m.options.Chats.Destinations {
				err = m.bot.ForwardMessages(chat, destination, messages, noQuote)
				if err != nil {
					return errors.Wrap(err, "forwarding a msg")
				}
			}

			// FIXME: Maybe move this up, so we don't retry to forward on errors
			err = db.UpdateForwarded(m.db, chat.ID, messageId)
			if err != nil {
				return errors.Wrap(err, "updating forwarded status")
			}
		}
	}

	return nil
}

func (m Monitor) UpdateMessage(chat tg.InputChannel, msg db.Message) (db.Message, error) {
	newText, err := m.bot.GetMessageText(chat, msg.ID)
	if err != nil {
		return db.Message{}, errors.Wrap(err, "getting new message text")
	}

	if msg.Body != newText {
		fmt.Println("message text is different, updating message")
		msg.Body = newText
		err = db.UpdateMessageBody(m.db, msg)
		if err != nil {
			return db.Message{}, errors.Wrap(err, "updating message body with new one")
		}
	}

	return msg, nil
}

func (m Monitor) syncReactions(new tg.MessageReactions, msg db.Message, accessHash int64) (reactions []db.Reaction, err error) {
	old, err := db.GetSavedReactions(m.db, msg.ChatID, msg.ID)
	if err != nil {
		return nil, errors.Wrap(err, "getting saved reactions")
	}

	totalReactions := 0
	for _, result := range new.Results {
		totalReactions += result.Count
	}

	// Check if we can trust "recent reactions"
	// If so - save recent reactions
	if len(new.RecentReactions) == totalReactions {
		reactions, err = helpers.AsReactions(new.RecentReactions, msg.ChatID, msg.ID)
		if err != nil {
			return nil, errors.Wrap(err, "converting reaction")
		}

		err = db.SyncPeerReactions(m.db, old, reactions)
		if err != nil {
			return nil, errors.Wrap(err, "syncing recent reactions")
		}
	} else {
		// If we can't use recent reaction, we have to request reactionsList
		reactionsList, err := m.bot.GetReactionsList(msg, accessHash)
		if err != nil {
			return nil, errors.Wrap(err, "getting reactions list from telegram")
		}

		reactions, err = helpers.AsReactions(reactionsList.Reactions, msg.ChatID, msg.ID)
		if err != nil {
			return nil, errors.Wrap(err, "converting reaction")
		}

		err = db.SyncPeerReactions(m.db, old, reactions)
		if err != nil {
			return nil, errors.Wrap(err, "syncing reactions reactions")
		}
	}

	return reactions, nil
}

func (m Monitor) rateMessage(reactions []db.Reaction, msg db.Message) (int, error) {
	usersReactions := make(map[int64]int)
	for _, reaction := range reactions {
		if _, ok := usersReactions[reaction.UserID]; !ok {
			emotePositivity := 8 // FIXME: Hardcoded value

			if reaction.DocumentID != 0 {
				usersReactions[reaction.UserID] = emotePositivity
				continue
			}

			emotePositivity, err := helpers.ReactionPositivity(reaction.Emoticon)
			if err != nil {
				fmt.Println("error getting reaction positivity:", err, "for message id:", msg.ID)
				emotePositivity = 1
			}
			usersReactions[reaction.UserID] = emotePositivity
		}
	}

	replies, err := db.GetReplies(m.db, msg.ChatID, msg.ID)
	if err != nil {
		return 0, errors.Wrap(err, "getting replies from database")
	}

	positiveRepliedUsers, err := helpers.PositiveReplies(replies)
	if err != nil {
		return 0, errors.Wrap(err, "getting positive replied users")
	}

	for userID := range positiveRepliedUsers {
		if _, ok := usersReactions[userID]; !ok {
			// Replied to msg - 8
			usersReactions[userID] = 8
		} else {
			// Replied and reacted to msg - 10
			usersReactions[userID] = 10
		}
	}

	totalRating := 0
	for _, reaction := range usersReactions {
		totalRating += reaction
	}

	stopWordCount := 0
	words := strings.Split(msg.Body, " ")
	for _, word := range words {
		if res, err := regexp.MatchString("(?i)(мяу)", word); res {
			stopWordCount += 1
		} else if err != nil {
			return 0, errors.Wrap(err, "matching stopWord")
		}
	}
	totalRating += stopWordCount * -10

	return totalRating, nil
}

func (m Monitor) ReplyMessageRating(
	e tg.Entities,
	u *tg.UpdateNewChannelMessage,
	replyID int,
	chat *tg.Channel,
) error {
	msg, err := db.GetMessage(m.db, chat.ID, replyID)
	if err != nil {
		return errors.Wrap(err, "getting saved message")
	}

	msg, err = m.UpdateMessage(tg.InputChannel{
		ChannelID:  chat.ID,
		AccessHash: chat.AccessHash,
	}, msg)

	reactionsList, err := m.bot.GetReactionsList(msg, chat.AccessHash)
	if err != nil {
		return errors.Wrap(err, "getting reactions list for a message")
	}
	reactions, err := helpers.AsReactions(reactionsList.Reactions, msg.ChatID, msg.ID)
	if err != nil {
		return errors.Wrap(err, "converting reaction")
	}

	totalRating, err := m.rateMessage(reactions, msg)
	if err != nil {
		return errors.Wrap(err, "rating message")
	}

	err = m.bot.Reply(e, u, fmt.Sprint(totalRating))
	if err != nil {
		return errors.Wrap(err, "replying with rating")
	}

	return nil
}

func (m Monitor) checkForMissedMessages() error {
	chats, err := db.GetOnlySavedChats(m.options.Chats.Sources, m.db)
	if err != nil {
		return errors.Wrap(err, "getting saved chats from database")
	}

	for _, chat := range chats {
		missingRanges, err := db.GetMissedMessagesRanges(chat.ID, m.db)
		if err != nil {
			return errors.Wrap(err, "getting missed messages ranges")
		}

		fmt.Println("missing ranges:", missingRanges)

		for _, missingRange := range missingRanges {
			start := missingRange[0]
			limit := missingRange[1] - missingRange[0] + 1

			fmt.Printf("start: %d, limit: %d\n", start, limit)

			// Split range into multiple calls if limit is larger than 100
			for offset := start; offset < start+limit; offset += 100 {
				fmt.Println("checking missing range:", missingRange, "with offset:", offset)

				// Calculate the actual limit for this call based on remaining messages
				callLimit := limit
				if remaining := start + limit - offset; remaining < 100 {
					callLimit = remaining
				} else {
					callLimit = 100

					fmt.Println("sleeping for 20 minutes")
					time.Sleep(20 * time.Minute)
				}

				part, err := m.bot.GetHistory(chat.ID, chat.AccessHash, callLimit, offset)
				if err != nil {
					return errors.Wrap(err, "getting messages")
				}

				fmt.Println("got", len(part), "messages")

				var savedMessages []db.Message

				for _, messageClass := range part {
					var message *tg.Message
					switch v := messageClass.(type) {
					case *tg.Message:
						message = v
					case *tg.MessageService:
						continue
					case *tg.MessageEmpty:
						continue
					default:
						return errors.New("unexpected message type")
					}

					// If message is not in missing range - skip it
					if message.ID < missingRange[0] || message.ID > missingRange[1] {
						continue
					}

					// Save message
					dbMessage, err := db.SaveMessage(message, chat.ID, m.db)
					if err != nil {
						return errors.Wrap(err, "saving message")
					}
					savedMessages = append(savedMessages, dbMessage)
				}

				err = m.checkMessages(chat, savedMessages)
				if err != nil {
					return errors.Wrap(err, "checking messages")
				}

				// Sleep for 3 seconds to avoid hitting the rate limit
				time.Sleep(3 * time.Second)
			}

			err = db.MarkRangeAsChecked(chat.ID, missingRange[0], missingRange[1], m.db)
			if err != nil {
				return errors.Wrap(err, "marking range as checked")
			}

			fmt.Println("finished checking missing range:", missingRange)

			fmt.Println("sleeping for 3 hours")
			time.Sleep(3 * time.Hour)
		}
	}

	return nil
}
