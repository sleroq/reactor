package main

import (
	"context"
	"database/sql"
	"encoding/json"
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

func sessionFolder(phone string) string {
	var out []rune
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	return "phone-" + string(out)
}

func getReactions(ctx context.Context, client *telegram.Client, chatId int64, accessHash int64, messages []int) ([]Reaction, error) {
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

	var reactions []Reaction

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
			positive := true
			if strings.Contains(emoticon, "👎") ||
				strings.Contains(emoticon, "💩") ||
				strings.Contains(emoticon, "🤮") {
				positive = false
			}

			react := Reaction{
				UserId:     userId,
				MessageId:  int64(reactUpdate.MsgID),
				Positive:   positive,
				Emoticon:   emoticon,
				DocumentID: documentId,
				SentDate:   sentDate,
				Flags:      reaction.Flags,
				Big:        reaction.Big,
			}
			reactions = append(reactions, react)
		}
	}

	return reactions, nil
}

func Part[T any](slice []T, length int) (new []T, modified []T) {
	if length > len(slice) {
		length = len(slice)
	}
	return slice[:length], slice[length:]
}

func getMessagesReactions(ctx context.Context, client *telegram.Client, chat Chat, messages []Message) ([]Reaction, error) {
	var reactions []Reaction

	for len(messages) > 0 {
		var part []Message
		part, messages = Part(messages, 50)
		var messageIds []int
		for _, message := range part {
			messageIds = append(messageIds, int(message.Id))
		}

		fmt.Println("requesting reactions for", len(messageIds), "messages")
		someReactions, err := getReactions(ctx, client, chat.Id, chat.AccessHash, messageIds)
		if err != nil {
			return nil, errors.Wrap(err, "getting reactions")
		}

		reactions = append(reactions, someReactions...)
	}

	return reactions, nil
}

func saveReaction(db *sql.DB, reaction Reaction) error {
	_, err := db.Exec(`
		insert into reactions (
			messageId,
			userId,
			positive,
			emoticon,
			documentId,
			sentDate,
			flags,
			big
		) values (
		    :messageId,
		    :userId,
		    :positive,
			:emoticon,
		    :documentId,
			:sentDate,
			:flags,
			:big
		)`,
		reaction.MessageId,
		reaction.UserId,
		reaction.Positive,
		reaction.Emoticon,
		reaction.DocumentID,
		reaction.SentDate,
		reaction.Flags,
		reaction.Big,
	)

	if err != nil {
		return errors.Wrap(err, "inserting new reaction")
	}

	return nil
}

func positiveRepliedUsers(db *sql.DB, messageId, chatId int64) (users []int64, err error) {
	msgRows, err := db.Query(`
		select *
		from messages
		where replyTo = :messageId
			and chatId = :chatId
	`, messageId, chatId)
	if err != nil {
		return nil, errors.Wrap(err, "getting message replies")
	}

	var messages []Message
	for msgRows.Next() {
		var message Message
		err = msgRows.Scan(
			&message.Id,
			&message.UpdatedAt,
			&message.SentDate,
			&message.ChatId,
			&message.Forwarded,
			&message.ReplyTo,
			&message.UserId,
			&message.Body,
		)
		if err != nil {
			return nil, errors.Wrap(err, "scanning result")
		}

		messages = append(messages, message)
	}

	var usersReacted []int64

	for _, reply := range messages {
		body := strings.TrimSpace(reply.Body)
		positive := false

		if len(body) < 10 {
			if res, err := regexp.MatchString("(?i)(ор|лол|кек|хех)", body); res {
				positive = true
			} else if err != nil {
				return nil, errors.Wrap(err, "matching body")
			}
		}

		if positive && !slices.Contains(usersReacted, reply.UserId) {
			usersReacted = append(usersReacted, reply.UserId)
		}
	}

	return usersReacted, nil
}

func removeDuplicate[T string | int | int64](sliceList []T) []T {
	allKeys := make(map[T]bool)
	var list []T
	for _, item := range sliceList {
		if _, value := allKeys[item]; !value {
			allKeys[item] = true
			list = append(list, item)
		}
	}
	return list
}

func monitReactions(ctx context.Context, client *telegram.Client, db *sql.DB) error {
	for range time.Tick(time.Minute * 30) {
		startDate := time.Now().Add(-12 * time.Hour)

		chatRows, err := db.Query(`select * from chats`)
		var chats []Chat
		for chatRows.Next() {
			var chat Chat
			var body string
			err = chatRows.Scan(
				&chat.Id,
				&chat.UpdatedAt,
				&chat.CreatedAt,
				&chat.AccessHash,
				&body,
			)
			if err != nil {
				return errors.Wrap(err, "scanning result")
			}

			err = json.Unmarshal([]byte(body), &chat.Body)
			if err != nil {
				return errors.Wrap(err, "unmarshalling result")
			}

			chats = append(chats, chat)
		}

		for _, chat := range chats {
			msgRows, err := db.Query(`
				select * from messages
				where SentDate > :startDate
				and chatId = :chatId
				and forwarded = 0
			`, startDate, chat.Id)
			if err != nil {
				return errors.Wrap(err, "getting messages to check")
			}

			var messages []Message
			for msgRows.Next() {
				var message Message
				err = msgRows.Scan(
					&message.Id,
					&message.UpdatedAt,
					&message.SentDate,
					&message.ChatId,
					&message.Forwarded,
					&message.ReplyTo,
					&message.UserId,
					&message.Body,
				)
				if err != nil {
					return errors.Wrap(err, "scanning result")
				}

				messages = append(messages, message)
			}

			reactions, err := getMessagesReactions(ctx, client, chat, messages)
			if err != nil {
				return errors.Wrap(err, "getting reactions for all messages")
			}

			group := make(map[int64][]Reaction)
			for _, msg := range messages {
				group[msg.Id] = []Reaction{}
			}

			for _, r := range reactions {
				group[r.MessageId] = append(group[r.MessageId], r)
			}
			// Update reactions per-message
			for messageId, reacts := range group {
				oldReactions, err := getSavedReactions(db, chat.Id, messageId)
				if err != nil {
					return errors.Wrap(err, "getting saved reactions")
				}

				var positiveReactedUsers []int64
				for _, react := range reacts {
					hasNewReaction := slices.ContainsFunc(oldReactions, func(el Reaction) bool {
						if el.SentDate.Equal(react.SentDate) &&
							el.UserId == react.UserId &&
							el.MessageId == react.MessageId {
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

					if react.Positive && !slices.Contains(positiveReactedUsers, react.UserId) {
						positiveReactedUsers = append(positiveReactedUsers, react.UserId)
					}
				}

				for _, oldReact := range oldReactions {
					hasOldReaction := slices.ContainsFunc(reacts, func(el Reaction) bool {
						if el.SentDate.Equal(oldReact.SentDate) &&
							el.UserId == oldReact.UserId &&
							el.MessageId == oldReact.MessageId {
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

				positiveRepliedUsers, err := positiveRepliedUsers(db, messageId, chat.Id)
				if err != nil {
					return errors.Wrap(err, "getting positive replied users")
				}

				totalPositive := append(positiveReactedUsers, positiveRepliedUsers...)
				totalPositive = removeDuplicate(totalPositive)

				if len(totalPositive) > 2 {
					fmt.Println(
						"forwarding message ", messageId,
						"with", len(totalPositive), "positive interactions",
					)

					err = forwardMessage(ctx, client, chat, messageId)
					if err != nil {
						return errors.Wrap(err, "forwarding a message")
					}

					err = updateForwarded(db, chat.Id, messageId)
					if err != nil {
						return errors.Wrap(err, "updating forwarded status")
					}
				}
			}
		}
	}

	return nil
}

func updateForwarded(db *sql.DB, chatId int64, messageId int64) error {
	_, err := db.Exec(`
		update messages
		set forwarded = 1
		where chatId = :chatId
			and id = :messageId
	`, chatId, messageId)

	return err
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
				ChannelID:  chat.Id,
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

func deleteReaction(db *sql.DB, react Reaction) error {
	_, err := db.Exec(`
		delete from reactions
		where messageId = :messageId
			and userId = :userId
			and sentDate = :SentDate
	`, react.MessageId, react.UserId, react.SentDate)

	return err
}

func getSavedReactions(db *sql.DB, chatId int64, messageId int64) ([]Reaction, error) {
	rows, err := db.Query(`
		select 
			r.messageId,
			r.userId,
			r.positive,
			r.emoticon,
			r.documentId,
			r.sentDate,
			r.flags,
			r.big
		from reactions r,
		     messages m
		where r.messageId = m.id
			and m.id = :messageId
			and m.chatId = :chatId
	`, messageId, chatId)
	if err != nil {
		return nil, errors.Wrap(err, "querying reactions for message")
	}

	var reactions []Reaction
	for rows.Next() {
		var reaction Reaction
		err = rows.Scan(
			&reaction.MessageId,
			&reaction.UserId,
			&reaction.Positive,
			&reaction.Emoticon,
			&reaction.DocumentID,
			&reaction.SentDate,
			&reaction.Flags,
			&reaction.Big,
		)
		if err != nil {
			return nil, errors.Wrap(err, "scanning result")
		}

		reactions = append(reactions, reaction)
	}

	return reactions, nil
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

func saveMessage(msg *tg.Message, chatId int64, db *sql.DB) error {
	sentDate := time.Unix(int64(msg.Date), 0)

	var userId int64
	switch v := msg.FromID.(type) {
	case *tg.PeerUser:
		userId = v.UserID
	default:
		return fmt.Errorf("unexpected message sender type")
	}

	_, err := db.Exec(`
		insert into messages (
		    id,
		    sentDate,
		    chatId,
		    replyTo,
			userId,
		    body
		) values (
		    :id,
			:sentDate,
			:chatId,
			:replyTo,
		    :userId,
		    :body
		)
	`, msg.ID, sentDate, chatId, msg.ReplyTo.ReplyToMsgID, userId, msg.Message)
	if err != nil {
		return errors.Wrap(err, "saving message to database")
	}

	return nil
}

func saveChat(channel *tg.Channel, db *sql.DB) error {
	body, err := json.Marshal(channel)
	if err != nil {
		return errors.Wrap(err, "marshalling chat data")
	}

	_, err = db.Exec(`
		insert or ignore into chats (
		    id,
		    accessHash,
		    body
		) values (
		    :id,
		    :accessHash,
		    :body
		)
	`, channel.ID, channel.AccessHash, body)
	if err != nil {
		return errors.Wrap(err, "saving chat to database")
	}

	return nil
}

type Chat struct {
	Id         int64
	AccessHash int64
	UpdatedAt  time.Time
	CreatedAt  time.Time
	Body       map[string]any
}

type Message struct {
	Id        int64
	UpdatedAt time.Time
	SentDate  time.Time
	ChatId    int64
	Forwarded bool
	ReplyTo   int64
	UserId    int64
	Body      string
}

type Reaction struct {
	UserId     int64
	MessageId  int64
	Positive   bool
	Emoticon   string
	DocumentID int64
	SentDate   time.Time
	Flags      bin.Fields
	Big        bool
}

func setupDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", "./memoq.db")
	if err != nil {
		return nil, errors.Wrap(err, "opening sqlite database")
	}

	_, err = db.Exec(`
		create table if not exists chats (
			id integer not null primary key,
			updatedAt datetime default (datetime('now')),
			createdAt datetime default (datetime('now')),
		    accessHash integer not null,
			body text
		);
	`)
	if err != nil {
		return nil, errors.Wrap(err, "creating chats table")
	}

	_, err = db.Exec(`
		create table if not exists messages (
			id integer not null primary key,
			updatedAt datetime default (datetime('now')),
			sentDate timestamp not null,
			chatId integer not null,
		    forwarded integer default 0,
		    replyTo integer default 0,
		    userId integer not null,
			body text not null,
		    foreign key(chatId) references chats(id)
		);
	`)
	if err != nil {
		return nil, errors.Wrap(err, "creating messages table")
	}

	_, err = db.Exec(`
		create table if not exists reactions (
			messageId integer not null,
			userId integer not null,
			positive integer not null,
			emoticon text,
			documentId integer,
			SentDate datetime not null,
			Flags integer not null,
			Big integer not null,
		    foreign key(messageId) references messages(id)
		);
	`)
	if err != nil {
		return nil, errors.Wrap(err, "creating reactions table")
	}

	return db, nil
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
