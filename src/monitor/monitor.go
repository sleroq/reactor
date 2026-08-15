package monitor

import (
	"context"
	"database/sql"
	"fmt"
	"go.uber.org/zap"
	"slices"
	"sync"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	"github.com/sleroq/reactor/src/bot"
	"regexp"
	"strings"
	"time"

	"github.com/sleroq/reactor/src/db"
	"github.com/sleroq/reactor/src/helpers"
)

type Thresholds struct {
	Text       int
	Photo      int
	Forward    int
	TextMax    int
	PhotoMax   int
	ForwardMax int
}
type Chats struct {
	Sources      []tg.InputPeerChannel
	Destinations []tg.InputPeerClass
}

type Options struct {
	Thresholds             Thresholds
	ThresholdHistoryWindow time.Duration
	ThresholdMaturityAge   time.Duration
	TargetForwardsPerDay   int
	Chats                  Chats
	NoQuoteWhitelist       []int64
}

type Monitor struct {
	db              *sql.DB
	bot             *bot.Bot
	options         Options
	mu              *sync.Mutex
	thresholdsMu    *sync.Mutex
	thresholdsCache map[int64]cachedThresholds
	logger          *zap.SugaredLogger
}

const MsgReqDelay = 30 * time.Second
const RecoveringDelay = 5 * time.Minute
const thresholdCacheDuration = time.Hour

type messageCategory int

const (
	textCategory messageCategory = iota
	photoCategory
	forwardCategory
)

type cachedThresholds struct {
	values    map[messageCategory]int
	expiresAt time.Time
}

var stopWordPattern = regexp.MustCompile(`(?i)(мяу)`)

func New(options Options, db *sql.DB, bot *bot.Bot, parentLogger *zap.SugaredLogger) *Monitor {
	logger := parentLogger.Named("monitor")
	return &Monitor{
		db,
		bot,
		options,
		&sync.Mutex{},
		&sync.Mutex{},
		make(map[int64]cachedThresholds),
		logger,
	}
}

func (m Monitor) RecoverSync() error {
	errChan := make(chan error)

	go func() {
		errChan <- m.checkForMissedMessages()
	}()

	err := <-errChan

	return err
}

// WatchSync runs reactions & replies monitor
// delay - duration between each check
// ageLimit - duration in which messages will be monitored
func (m Monitor) WatchSync(ctx context.Context, delay time.Duration, ageLimit time.Duration) {
	go func() {
		ticker := time.NewTicker(delay)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				m.logger.Info("Stopping WatchSync")
				return
			case <-ticker.C:
				if err := m.checkMessagesSafely(ageLimit); err != nil {
					m.logger.Errorf("error checking for new messages: %s", err)
				}
			}
		}
	}()
}

// WatchAsync runs reactions & replies monitor
// with mutex to avoid running multiple instances
func (m Monitor) checkMessagesSafely(ageLimit time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.checkForNewMessages(ageLimit)
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

		if len(messages) == 0 {
			continue
		}

		err = m.checkMessages(chat, messages)
		if err != nil {
			return errors.Wrap(err, "checking messages")
		}
	}

	return nil
}

func (m Monitor) checkMessages(chat db.Chat, messages []db.Message) error {
	reactionUpdates, err := m.bot.GetMessagesReactions(chat, messages, MsgReqDelay, m.logger)
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

		thresholds, err := m.dynamicThresholds(chat.ID, time.Now())
		if err != nil {
			return errors.Wrap(err, "calculating dynamic thresholds")
		}
		threshold := thresholds[categoryOf(msg)]

		if totalRating > threshold {
			// Checking to see if message was edited
			msg, err = m.UpdateMessage(tg.InputChannel{
				ChannelID:  chat.ID,
				AccessHash: chat.AccessHash,
			}, msg)
			if err != nil {
				return errors.Wrap(err, "updating message")
			}

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
			for _, id := range m.options.NoQuoteWhitelist {
				if id == msg.FwdFromChannel || id == msg.FwdFromUser || id == msg.UserID {
					noQuote = false
					break
				}
			}

			messages := []db.Message{msg}
			if msg.GroupedID != 0 {
				messages, err = db.GetMessagesGroup(m.db, msg.GroupedID)
				if err != nil {
					return errors.Wrap(err, "getting grouped messages")
				}
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
		reactions, err = m.asReactions(new.RecentReactions, msg)
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

		reactions, err = m.asReactions(reactionsList.Reactions, msg)
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

func (m Monitor) asReactions(tgReactions []tg.MessagePeerReaction, msg db.Message) ([]db.Reaction, error) {
	documentIDs := make([]int64, 0)
	for _, reaction := range tgReactions {
		if custom, ok := reaction.Reaction.(*tg.ReactionCustomEmoji); ok {
			documentIDs = append(documentIDs, custom.DocumentID)
		}
	}

	customEmoji, err := m.bot.GetCustomEmoji(documentIDs)
	if err != nil {
		return nil, errors.Wrap(err, "getting custom emoji")
	}

	reactions, err := helpers.AsReactions(tgReactions, customEmoji, msg.ChatID, msg.ID)
	if err != nil {
		return nil, errors.Wrap(err, "converting reaction")
	}
	return reactions, nil
}

func (m Monitor) rateMessage(reactions []db.Reaction, msg db.Message) (int, error) {
	return m.rateMessageAt(reactions, msg, time.Time{})
}

func (m Monitor) rateMessageAt(reactions []db.Reaction, msg db.Message, cutoff time.Time) (int, error) {
	replies, err := db.GetReplies(m.db, msg.ChatID, msg.ID)
	if err != nil {
		return 0, errors.Wrap(err, "getting replies from database")
	}
	return rateMessageWithReplies(reactions, replies, msg, cutoff)
}

func rateMessageWithReplies(reactions []db.Reaction, replies []db.Message, msg db.Message, cutoff time.Time) (int, error) {
	usersReactions := make(map[int64]int)
	for _, reaction := range reactions {
		if !cutoff.IsZero() && reaction.SentDate.After(cutoff) {
			continue
		}
		if _, ok := usersReactions[reaction.UserID]; !ok {
			emotePositivity := 8
			if reaction.DocumentID == 0 {
				var err error
				emotePositivity, err = helpers.ReactionPositivity(reaction.Emoticon)
				if err != nil {
					fmt.Println("error getting reaction positivity:", err, "for message id:", msg.ID)
					emotePositivity = 1
				}
			} else if positivity, err := helpers.ReactionPositivity(reaction.Emoticon); err == nil && positivity < 0 {
				emotePositivity = positivity
			}
			usersReactions[reaction.UserID] = emotePositivity
		}
	}

	if !cutoff.IsZero() {
		replies = slices.DeleteFunc(replies, func(reply db.Message) bool {
			return reply.SentDate.After(cutoff)
		})
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

	stopWordCount := countStopWords(msg.Body)
	for _, reply := range replies {
		if reply.UserID == msg.UserID {
			stopWordCount += countStopWords(reply.Body)
		}
	}
	totalRating += stopWordCount * -10

	return totalRating, nil
}

func countStopWords(text string) int {
	count := 0
	for word := range strings.SplitSeq(text, " ") {
		if stopWordPattern.MatchString(word) {
			count++
		}
	}
	return count
}

func categoryOf(msg db.Message) messageCategory {
	if msg.FwdFromChannel != 0 || msg.FwdFromUser != 0 {
		return forwardCategory
	}
	if msg.WithPhoto {
		return photoCategory
	}
	return textCategory
}

func (m Monitor) dynamicThresholds(chatID int64, now time.Time) (map[messageCategory]int, error) {
	m.thresholdsMu.Lock()
	defer m.thresholdsMu.Unlock()

	if cached, ok := m.thresholdsCache[chatID]; ok && now.Before(cached.expiresAt) {
		return cached.values, nil
	}

	messages, err := db.GetMessagesBetween(
		m.db,
		chatID,
		now.Add(-m.options.ThresholdHistoryWindow-m.options.ThresholdMaturityAge),
		now.Add(-m.options.ThresholdMaturityAge),
	)
	if err != nil {
		return nil, errors.Wrap(err, "getting mature messages")
	}
	reactions, err := db.GetReactionsForMessagesBetween(
		m.db,
		chatID,
		now.Add(-m.options.ThresholdHistoryWindow-m.options.ThresholdMaturityAge),
		now.Add(-m.options.ThresholdMaturityAge),
	)
	if err != nil {
		return nil, errors.Wrap(err, "getting reactions for mature messages")
	}
	replies, err := db.GetRepliesForMessagesBetween(
		m.db,
		chatID,
		now.Add(-m.options.ThresholdHistoryWindow-m.options.ThresholdMaturityAge),
		now.Add(-m.options.ThresholdMaturityAge),
	)
	if err != nil {
		return nil, errors.Wrap(err, "getting replies for mature messages")
	}

	reactionsByMessage := make(map[int][]db.Reaction)
	for _, reaction := range reactions {
		reactionsByMessage[reaction.MessageID] = append(reactionsByMessage[reaction.MessageID], reaction)
	}
	repliesByMessage := make(map[int][]db.Message)
	for _, reply := range replies {
		repliesByMessage[reply.ReplyTo] = append(repliesByMessage[reply.ReplyTo], reply)
	}

	ratings := map[messageCategory][]int{
		textCategory:    {},
		photoCategory:   {},
		forwardCategory: {},
	}
	for _, msg := range messages {
		rating, err := rateMessageWithReplies(
			reactionsByMessage[msg.ID],
			repliesByMessage[msg.ID],
			msg,
			msg.SentDate.Add(m.options.ThresholdMaturityAge),
		)
		if err != nil {
			return nil, errors.Wrap(err, "rating threshold sample")
		}
		category := categoryOf(msg)
		ratings[category] = append(ratings[category], rating)
	}

	thresholds := make(map[messageCategory]int, 3)
	targetCount := m.options.TargetForwardsPerDay * int(m.options.ThresholdHistoryWindow/(24*time.Hour))
	thresholds[textCategory] = percentileThreshold(ratings[textCategory], len(messages), targetCount, m.options.Thresholds.Text, m.options.Thresholds.TextMax)
	thresholds[photoCategory] = percentileThreshold(ratings[photoCategory], len(messages), targetCount, m.options.Thresholds.Photo, m.options.Thresholds.PhotoMax)
	thresholds[forwardCategory] = percentileThreshold(ratings[forwardCategory], len(messages), targetCount, m.options.Thresholds.Forward, m.options.Thresholds.ForwardMax)

	m.thresholdsCache[chatID] = cachedThresholds{values: thresholds, expiresAt: now.Add(thresholdCacheDuration)}
	m.logger.Infow("calculated dynamic thresholds",
		"chat_id", chatID,
		"samples", len(messages),
		"text", thresholds[textCategory],
		"photo", thresholds[photoCategory],
		"forward", thresholds[forwardCategory],
	)
	return thresholds, nil
}

func percentileThreshold(ratings []int, totalSamples, targetCount, minimum, maximum int) int {
	if len(ratings) == 0 || totalSamples <= targetCount {
		return minimum
	}

	slices.Sort(ratings)
	// Use the same percentile for every category, so their expected forwards
	// add up to the target rather than each category producing the target.
	index := (len(ratings)*(totalSamples-targetCount) + totalSamples - 1) / totalSamples
	index--
	threshold := ratings[max(index, 0)]
	return min(max(threshold, minimum), maximum)
}

func (m Monitor) MessageRating(chatID int64, messageID int) (rating, threshold int, err error) {
	m.logger.Infof("calculating rating for message %d", messageID)

	msg, err := db.GetMessage(m.db, chatID, messageID)
	if err != nil {
		return 0, 0, err
	}
	chats, err := db.GetOnlySavedChats([]tg.InputPeerChannel{{ChannelID: chatID}}, m.db)
	if err != nil {
		return 0, 0, errors.Wrap(err, "getting saved chat")
	}
	if len(chats) == 0 {
		return 0, 0, sql.ErrNoRows
	}
	chat := chats[0]

	msg, err = m.UpdateMessage(tg.InputChannel{
		ChannelID:  chat.ID,
		AccessHash: chat.AccessHash,
	}, msg)
	if err != nil {
		return 0, 0, errors.Wrap(err, "updating message")
	}

	reactionsList, err := m.bot.GetReactionsList(msg, chat.AccessHash)
	if err != nil {
		return 0, 0, errors.Wrap(err, "getting reactions list for a message")
	}
	reactions, err := m.asReactions(reactionsList.Reactions, msg)
	if err != nil {
		return 0, 0, errors.Wrap(err, "converting reaction")
	}

	totalRating, err := m.rateMessage(reactions, msg)
	if err != nil {
		return 0, 0, errors.Wrap(err, "rating message")
	}

	thresholds, err := m.dynamicThresholds(chatID, time.Now())
	if err != nil {
		return 0, 0, errors.Wrap(err, "calculating dynamic threshold")
	}

	return totalRating, thresholds[categoryOf(msg)], nil
}

func (m Monitor) checkForMissedMessages() error {
	logger := m.logger.Named("recovering")

	chats, err := db.GetOnlySavedChats(m.options.Chats.Sources, m.db)
	if err != nil {
		return errors.Wrap(err, "getting saved chats from database")
	}

	for _, chat := range chats {
		missingRanges, err := db.GetMissedMessagesRanges(chat.ID, m.db)
		if err != nil {
			return errors.Wrap(err, "getting missed messages ranges")
		}

		logger.Infof("checking chat %d for missing messages", chat.ID)
		logger.Debugf("missing ranges: %v", missingRanges)

		for _, missingRange := range missingRanges {
			start := missingRange[0]
			limit := missingRange[1] - missingRange[0] + 1

			logger.Debugf("start: %d, limit: %d", start, limit)

			// Split range into multiple calls if limit is larger than 100
			for offset := start; offset < start+limit; offset += 100 {
				logger.Debugf("checking missing range: %v with offset: %d", missingRange, offset)

				// Calculate the actual limit for this call based on remaining messages
				var callLimit int
				if remaining := start + limit - offset; remaining < 100 {
					callLimit = remaining
				} else {
					callLimit = 100

					logger.Debugf("sleeping for %s, to make recovering slow", RecoveringDelay)
					time.Sleep(RecoveringDelay)
				}

				part, err := m.bot.GetHistory(chat.ID, chat.AccessHash, callLimit, offset)
				if err != nil {
					return errors.Wrap(err, "getting messages")
				}

				logger.Debugf("got %d messages from telegram", len(part))

				var savedMessages []db.Message

				for _, messageClass := range part {
					var message *tg.Message
					switch v := messageClass.(type) {
					case *tg.Message:
						message = v
					case *tg.MessageService:
						logger.Infof("skipping service message: %s", helpers.FormatObject(v))
						continue
					case *tg.MessageEmpty:
						logger.Infof("skipping empty message: %s", helpers.FormatObject(v))
						continue
					default:
						return errors.New("unexpected message type")
					}

					// If message is not in missing range - skip it
					if message.ID < missingRange[0] || message.ID > missingRange[1] {
						logger.Warn("skipping message, because it's not in missing range (how?):", message.ID)
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
			}

			err = db.MarkRangeAsChecked(chat.ID, missingRange[0], missingRange[1], m.db)
			if err != nil {
				return errors.Wrap(err, "marking range as checked")
			}

			logger.Infof("finished checking missing range with %d messages", limit)

			logger.Debugf("sleeping for %s, to make recovering slow", RecoveringDelay)
			time.Sleep(RecoveringDelay)
		}
	}

	return nil
}
