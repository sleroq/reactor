package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	pebbledb "github.com/cockroachdb/pebble"
	"github.com/go-faster/errors"
	boltstor "github.com/gotd/contrib/bbolt"
	"github.com/gotd/contrib/middleware/floodwait"
	"github.com/gotd/contrib/middleware/ratelimit"
	"github.com/gotd/contrib/pebble"
	"github.com/gotd/contrib/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	_ "github.com/mattn/go-sqlite3"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/exp/slices"
	"golang.org/x/time/rate"
	lj "gopkg.in/natefinch/lumberjack.v2"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type TelegramReaction struct {
	UserID     int64
	MessageID  int64
	Emoticon   string
	DocumentID int64
	SentDate   time.Time
	Flags      bin.Fields
	Big        bool
	Positivity int
}

func getReactions(ctx context.Context, client *telegram.Client, chatId int64, accessHash int64, messages []int) ([]TelegramReaction, error) {
	update, err := client.API().MessagesGetMessagesReactions(ctx, &tg.MessagesGetMessagesReactionsRequest{
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

	var reactions []TelegramReaction

	for _, reactUpdate := range reactionUpdates {
		for _, reaction := range reactUpdate.Reactions.RecentReactions {
			var emoticon string
			var documentId int64

			switch r := reaction.Reaction.(type) {
			case *tg.ReactionEmoji:
				emoticon = r.GetEmoticon()
			case *tg.ReactionCustomEmoji:
				documentId = r.GetDocumentID()
			default:
				return nil, fmt.Errorf("unexpected reaction type: %s", reaction.String())
			}

			var userId int64
			switch p := reaction.PeerID.(type) {
			case *tg.PeerUser:
				userId = p.UserID
			default:
				return nil, fmt.Errorf("unexpected peer type: %s", p)
			}

			sentDate := time.Unix(int64(reaction.Date), 0)
			positivity := reactionPositivity(emoticon)

			react := TelegramReaction{
				UserID:     userId,
				MessageID:  int64(reactUpdate.MsgID),
				Emoticon:   emoticon,
				DocumentID: documentId,
				Positivity: positivity,
				SentDate:   sentDate,
				Flags:      reaction.Flags,
				Big:        reaction.Big,
			}
			reactions = append(reactions, react)
		}
	}

	return reactions, nil
}

func getMessagesReactions(ctx context.Context, client *telegram.Client, chat Chat, messages []Message) ([]TelegramReaction, error) {
	var reactions []TelegramReaction

	for len(messages) > 0 {
		var part []Message
		part, messages = Part(messages, 50)
		var messageIds []int
		for _, message := range part {
			messageIds = append(messageIds, int(message.ID))
		}

		fmt.Println("requesting reactions for", len(messageIds), "messages")
		someReactions, err := getReactions(ctx, client, chat.ID, chat.AccessHash, messageIds)
		if err != nil {
			return nil, errors.Wrap(err, "getting reactions")
		}

		reactions = append(reactions, someReactions...)
	}

	return reactions, nil
}

func positiveReplies(db *sql.DB, messageId, chatId int64) (map[int64]string, error) {
	messages, err := getReplies(db, chatId, messageId)
	if err != nil {
		return nil, errors.Wrap(err, "getting replies from database")
	}

	replies := make(map[int64]string)
	for _, reply := range messages {
		body := strings.TrimSpace(reply.Body)
		positive := false

		if len(body) < 10 {
			if res, err := regexp.MatchString("(?i)(ор|лол|кек|хех|жиза|база)", body); res {
				positive = true
			} else if err != nil {
				if res, err := regexp.MatchString("(?i)(секс|н.фега|вау|круто|абсолютно|абалдеть|ахнинеть|хорошо|красив|красав)", body); res {
					positive = true
				} else if err != nil {
					return nil, errors.Wrap(err, "matching body")
				}
				return nil, errors.Wrap(err, "matching body")
			}
		}
		if len(body) < 4 {
			if res, err := regexp.MatchString("(?i)(жиз|👍|❤️)", body); res {
				positive = true
			} else if err != nil {
				return nil, errors.Wrap(err, "matching body")
			}
		}

		if positive {
			replies[reply.UserID] = body
		}
	}

	return replies, nil
}

func monitReactions(ctx context.Context, client *telegram.Client, db *sql.DB) error {
	for range time.Tick(time.Minute * 5) {
		startDate := time.Now().Add(-12 * time.Hour)

		chats, err := getChats(db)
		if err != nil {
			return errors.Wrap(err, "getting chats from database")
		}

		for _, chat := range chats {
			messages, err := getMessagesAfter(db, chat.ID, startDate)
			if err != nil {
				return errors.Wrap(err, "getting messages from database")
			}

			reactions, err := getMessagesReactions(ctx, client, chat, messages)
			if err != nil {
				return errors.Wrap(err, "getting reactions for all messages")
			}

			group := make(map[int64][]TelegramReaction)
			messagesGroup := make(map[int64]Message)
			for _, msg := range messages {
				group[msg.ID] = []TelegramReaction{}
				messagesGroup[msg.ID] = msg
			}

			for _, r := range reactions {
				group[r.MessageID] = append(group[r.MessageID], r)
			}
			// Update reactions per-message
			for messageId, reacts := range group {
				message := messagesGroup[messageId]

				oldReactions, err := getSavedReactions(db, chat.ID, messageId)
				if err != nil {
					return errors.Wrap(err, "getting saved reactions")
				}

				err = syncReactions(db, oldReactions, reacts)
				if err != nil {
					return errors.Wrap(err, "syncing reactions")
				}

				// Ignore already forwarded messages
				if message.Forwarded {
					continue
				}

				usersReactions := make(map[int64]int)
				for _, reaction := range reacts {
					if _, ok := usersReactions[reaction.UserID]; !ok {
						usersReactions[reaction.UserID] = reaction.Positivity
					}
				}

				positiveRepliedUsers, err := positiveReplies(db, messageId, chat.ID)
				if err != nil {
					return errors.Wrap(err, "getting positive replied users")
				}

				for userID := range positiveRepliedUsers {
					if _, ok := usersReactions[userID]; !ok {
						// Replied to message - 8
						usersReactions[userID] = 8
					} else {
						// Replied and reacted to message - 10
						usersReactions[userID] = 10
					}
				}

				totalRating := 0
				for _, reaction := range usersReactions {
					totalRating += reaction
				}

				threshold := 24
				if !message.WithPhoto &&
					message.FwdFromChannel == 0 &&
					message.FwdFromUser == 0 {
					threshold = 30
				}

				if totalRating > threshold {
					fmt.Println(
						"forwarding message", messageId,
						"with", totalRating, "rating",
					)

					err = forwardMessage(ctx, client, chat, messageId)
					if err != nil {
						return errors.Wrap(err, "forwarding a message")
					}

					err = updateForwarded(db, chat.ID, messageId)
					if err != nil {
						return errors.Wrap(err, "updating forwarded status")
					}
				} else {
					fmt.Println(
						"skipping message with", totalRating, "rating",
					)
				}
			}
		}
	}

	return nil
}

func syncReactions(db *sql.DB, old []Reaction, new []TelegramReaction) (err error) {
	for _, react := range new {
		hasNewReaction := slices.ContainsFunc(old, func(el Reaction) bool {
			if el.SentDate.Equal(react.SentDate) &&
				el.UserID == react.UserID &&
				el.MessageID == react.MessageID {
				return true
			}
			return false
		})

		if !hasNewReaction {
			err = saveReaction(db, react)
			if err != nil {
				return errors.Wrap(err, "saving new reaction")
			}
		}
	}

	for _, oldReact := range old {
		hasOldReaction := slices.ContainsFunc(new, func(el TelegramReaction) bool {
			if el.SentDate.Equal(oldReact.SentDate) &&
				el.UserID == oldReact.UserID &&
				el.MessageID == oldReact.MessageID {
				return true
			}
			return false
		})
		if !hasOldReaction {
			err = deleteReaction(db, oldReact)
			if err != nil {
				return errors.Wrap(err, "deleting reaction")
			}
		}
	}

	return nil
}

func forwardMessage(ctx context.Context, client *telegram.Client, chat Chat, messageId int64) error {
	source := rand.NewSource(time.Now().UnixNano())
	generator := rand.New(source)

	channelId, err := strconv.ParseInt(os.Getenv("CHANNEL_ID"), 10, 64)
	if err != nil {
		return errors.Wrap(err, "can't parse CHANNEL_ID")
	}
	channelAccessHash, err := strconv.ParseInt(os.Getenv("CHANNEL_ACCESS_HASH"), 10, 64)
	if err != nil {
		return errors.Wrap(err, "can't parse CHANNEL_ACCESS_HASH")
	}

	_, err = client.API().MessagesForwardMessages(
		ctx,
		&tg.MessagesForwardMessagesRequest{
			Flags:             0,
			Silent:            false,
			Background:        false,
			WithMyScore:       false,
			DropAuthor:        true,
			DropMediaCaptions: false,
			Noforwards:        false,
			FromPeer: &tg.InputPeerChannel{
				ChannelID:  chat.ID,
				AccessHash: chat.AccessHash,
			},
			ID:       []int{int(messageId)},
			RandomID: []int64{generator.Int63()},
			ToPeer: &tg.InputPeerChannel{
				ChannelID:  channelId,
				AccessHash: channelAccessHash,
			},
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

func run(ctx context.Context) error {
	var arg struct {
		FillPeerStorage bool
	}
	flag.BoolVar(&arg.FillPeerStorage, "fill-peer-storage", false, "fill peer storage")
	flag.Parse()

	// TG_PHONE is phone number in international format.
	// Like +4123456789.
	phone := os.Getenv("TG_PHONE")
	if phone == "" {
		return errors.New("no phone")
	}
	// APP_HASH, APP_ID is from https://my.telegram.org/.
	appID, err := strconv.Atoi(os.Getenv("APP_ID"))
	if err != nil {
		return errors.Wrap(err, " parse app id")
	}
	appHash := os.Getenv("APP_HASH")
	if appHash == "" {
		return errors.New("no app hash")
	}

	// Setting up session storage.
	// This is needed to reuse session and not login every time.
	sessionDir := filepath.Join("session", sessionFolder(phone))
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		return err
	}
	logFilePath := filepath.Join(sessionDir, "log.jsonl")

	selectedChatId, err := strconv.ParseInt(os.Getenv("CHAT_ID"), 10, 64)
	if err != nil {
		return errors.New("can't parse CHAT_ID")
	}

	fmt.Printf("Storing session in %s, logs in %s\n", sessionDir, logFilePath)

	// Setting up logging to file with rotation.
	//
	// Log to file, so we don't interfere with prompts and messages to user.
	logWriter := zapcore.AddSync(&lj.Logger{
		Filename:   logFilePath,
		MaxBackups: 3,
		MaxSize:    1, // megabytes
		MaxAge:     7, // days
	})
	logCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		logWriter,
		zap.DebugLevel,
	)
	lg := zap.New(logCore)
	defer func() { _ = lg.Sync() }()

	// So, we are storing session information in current directory, under subdirectory "session/phone_hash"
	sessionStorage := &telegram.FileSessionStorage{
		Path: filepath.Join(sessionDir, "session.json"),
	}
	// Peer storage, for resolve caching and short updates handling.
	db, err := pebbledb.Open(filepath.Join(sessionDir, "peers.pebble.db"), &pebbledb.Options{})
	if err != nil {
		return errors.Wrap(err, "create pebble storage")
	}
	peerDB := pebble.NewPeerStorage(db)
	lg.Info("Storage", zap.String("path", sessionDir))

	// Setting up client.
	//
	// Dispatcher is used to register handlers for events.
	dispatcher := tg.NewUpdateDispatcher()
	// Setting up update handler that will fill peer storage before
	// calling dispatcher handlers.
	updateHandler := storage.UpdateHook(dispatcher, peerDB)

	// Setting up persistent storage for qts/pts to be able to
	// recover after restart.
	boltdb, err := bbolt.Open(filepath.Join(sessionDir, "updates.bolt.db"), 0666, nil)
	if err != nil {
		return errors.Wrap(err, "create bolt storage")
	}
	updatesRecovery := updates.New(updates.Config{
		Handler: updateHandler, // using previous handler with peerDB
		Logger:  lg.Named("updates.recovery"),
		Storage: boltstor.NewStateStorage(boltdb),
	})

	// Handler of FLOOD_WAIT that will automatically retry request.
	waiter := floodwait.NewWaiter().WithCallback(func(ctx context.Context, wait floodwait.FloodWait) {
		// Notifying about flood wait.
		lg.Warn("Flood wait", zap.Duration("wait", wait.Duration))
		fmt.Println("Got FLOOD_WAIT. Will retry after", wait.Duration)
	})

	// Filling client options.
	options := telegram.Options{
		Logger:         lg,              // Passing logger for observability.
		SessionStorage: sessionStorage,  // Setting up session sessionStorage to store auth data.
		UpdateHandler:  updatesRecovery, // Setting up handler for updates from server.
		Middlewares: []telegram.Middleware{
			// Setting up FLOOD_WAIT handler to automatically wait and retry request.
			waiter,
			// Setting up general rate limits to less likely get flood wait errors.
			ratelimit.New(rate.Every(time.Millisecond*100), 5),
		},
	}
	client := telegram.NewClient(appID, appHash, options)
	api := client.API()

	// Setting up resolver cache that will use peer storage.
	resolver := storage.NewResolverCache(peer.Plain(api), peerDB)
	_ = resolver

	// Setting up persistent storage for chats and messages
	botdb, err := setupDB()
	if err != nil {
		return errors.Wrap(err, "setting up the database")
	}
	defer botdb.Close()

	// Registering handler for new private messages in chats.
	dispatcher.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewChannelMessage) error {
		msg, ok := u.Message.(*tg.Message)
		if !ok {
			return nil
		}

		// Use PeerID to find peer because *Short updates does not contain any entities, so it necessary to
		// store some entities.
		//
		// Storage can be filled using PeerCollector (i.e. fetching all dialogs first).
		p, err := storage.FindPeer(ctx, peerDB, msg.GetPeerID())
		if err != nil {
			return err
		}

		if p.Channel == nil {
			return nil
		}

		// fmt.Println(msg.Message, p.Channel.ID, p.Channel.AccessHash)

		if p.Channel.ID != selectedChatId {
			return nil
		}

		err = saveChat(p.Channel, botdb)
		if err != nil {
			return errors.Wrap(err, "saving chat")
		}

		err = saveMessage(msg, p.Channel.ID, botdb)
		if err != nil {
			fmt.Println(err)
			return errors.Wrap(err, "saving message")
		}

		fmt.Printf("%s: %s\n", p, msg.Message)
		return nil
	})

	// Authentication flow handles authentication process, like prompting for code and 2FA password.
	flow := auth.NewFlow(Terminal{PhoneNumber: phone}, auth.SendCodeOptions{})

	return waiter.Run(ctx, func(ctx context.Context) error {
		go func() {
			err := monitReactions(ctx, client, botdb)
			if err != nil {
				fmt.Printf("Monitoring reactions: %s", err)
				return
			}
		}()

		// Spawning main goroutine.
		if err := client.Run(ctx, func(ctx context.Context) error {
			// Perform auth if no session is available.
			if err := client.Auth().IfNecessary(ctx, flow); err != nil {
				return errors.Wrap(err, "auth")
			}

			// Getting info about current user.
			self, err := client.Self(ctx)
			if err != nil {
				return errors.Wrap(err, "call self")
			}

			name := self.FirstName
			if self.Username != "" {
				// Username is optional.
				name = fmt.Sprintf("%s (@%s)", name, self.Username)
			}
			fmt.Println("Current user:", name)

			lg.Info("Login",
				zap.String("first_name", self.FirstName),
				zap.String("last_name", self.LastName),
				zap.String("username", self.Username),
				zap.Int64("id", self.ID),
			)

			if arg.FillPeerStorage {
				fmt.Println("Filling peer storage from dialogs to cache entities")
				collector := storage.CollectPeers(peerDB)
				if err := collector.Dialogs(ctx, query.GetDialogs(api).Iter()); err != nil {
					return errors.Wrap(err, "collect peers")
				}
				fmt.Println("Filled")
			}

			// Waiting until context is done.
			fmt.Println("Listening for updates. Interrupt (Ctrl+C) to stop.")
			return updatesRecovery.Run(ctx, api, self.ID, updates.AuthOptions{
				IsBot: self.Bot,
				OnStart: func(ctx context.Context) {
					fmt.Println("Update recovery initialized and started, listening for events")

				},
			})
		}); err != nil {
			return errors.Wrap(err, "run")
		}

		return nil
	})
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := run(ctx); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() == context.Canceled {
			fmt.Println("\rClosed")
			os.Exit(0)
		}
		_, _ = fmt.Fprintf(os.Stderr, "Error: %+v\n", err)
		os.Exit(1)
	} else {
		fmt.Println("Done")
		os.Exit(0)
	}
}
