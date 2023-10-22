package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Netflix/go-env"
	pebbledb "github.com/cockroachdb/pebble"
	"github.com/go-faster/errors"
	boltstor "github.com/gotd/contrib/bbolt"
	"github.com/gotd/contrib/middleware/floodwait"
	"github.com/gotd/contrib/middleware/ratelimit"
	"github.com/gotd/contrib/pebble"
	"github.com/gotd/contrib/storage"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	_ "github.com/mattn/go-sqlite3"
	botWrapper "github.com/sleroq/reactor/src/bot"
	"github.com/sleroq/reactor/src/db"
	"github.com/sleroq/reactor/src/monitor"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/time/rate"
	lj "gopkg.in/natefinch/lumberjack.v2"
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

type Int64Slice []int64

func (i *Int64Slice) UnmarshalEnvironmentValue(value string) error {
	parts := strings.Split(value, ",")
	for _, part := range parts {
		number, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			return err
		}
		*i = append(*i, number)
	}

	return nil
}

type Environment struct {
	Phone      string `env:"REACTOR_PHONE,required=true"`
	AppID      int    `env:"REACTOR_APP_ID,required=true"`
	AppHash    string `env:"REACTOR_APP_HASH,required=true"`
	SessionDir string `env:"REACTOR_SESSION_DIR,required=true"`

	WatchedChatIDs          Int64Slice `env:"REACTOR_CHAT_IDS,required=true"`
	DestChannelIDs          Int64Slice `env:"REACTOR_CHANNEL_ID,required=true"`
	DestChannelAccessHashes Int64Slice `env:"REACTOR_CHANNEL_ACCESS_HASH,required=true"`

	NoQuoteWhitelistIDs Int64Slice `env:"REACTOR_NOQUOTE_WHITELIST"`

	Thresholds struct {
		Text    int `env:"REACTOR_TEXT_THRESHOLD,default=31"`
		Photo   int `env:"REACTOR_PHOTO_THRESHOLD,default=23"`
		Forward int `env:"REACTOR_FORWARD_THRESHOLD,default=23"`
	}

	CheckFrequency struct {
		Wide   int `env:"REACTOR_WIDE_FREQUENCY,default=60"`
		Narrow int `env:"REACTOR_NARROW_FREQUENCY,default=10"`
	}
	CheckRange struct {
		Wide   int `env:"REACTOR_WIDE_RANGE,default=72"`
		Narrow int `env:"REACTOR_NARROW_RANGE,default=1"`
	}

	Recover bool `env:"REACTOR_RECOVER,default=true"`
}

type Options struct {
	Env              Environment
	ChatsToMonitor   []tg.InputPeerChannel
	NoQuoteWhitelist []int64
	DestChannels     []tg.InputPeerClass
}

func prepareInternalLogger(dir string) *zap.Logger {
	logFilePath := filepath.Join(dir, "log.jsonl")

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
	return zap.New(logCore)
}

func run(ctx context.Context, options Options, logger *zap.SugaredLogger) (err error) {
	// Setting up session storage.
	// This is needed to reuse session and not login every time.
	sessionDir := filepath.Join(options.Env.SessionDir, sessionFolder(options.Env.Phone))
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		return errors.Wrap(err, "create session dir")
	}

	lg := prepareInternalLogger(sessionDir)
	defer func() { _ = lg.Sync() }()

	// So, we are storing session information in current directory, under subdirectory "session/phone_hash"
	sessionStorage := &telegram.FileSessionStorage{
		Path: filepath.Join(sessionDir, "session.json"),
	}
	// Peer storage, for resolve caching and short updates handling.
	cacheDB, err := pebbledb.Open(filepath.Join(sessionDir, "peers.pebble.db"), &pebbledb.Options{})
	if err != nil {
		return errors.Wrap(err, "create pebble storage")
	}
	peerDB := pebble.NewPeerStorage(cacheDB)
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
		logger.Warn("Flood wait", zap.Duration("wait", wait.Duration))
	})

	// Filling client options.
	clientOptions := telegram.Options{
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
	client := telegram.NewClient(options.Env.AppID, options.Env.AppHash, clientOptions)
	api := client.API()

	// Setting up resolver cache that will use peer storage.
	resolver := storage.NewResolverCache(peer.Plain(api), peerDB)
	_ = resolver

	// Setting up persistent storage for chats and messages
	botDB, err := db.SetupDB()
	if err != nil {
		return errors.Wrap(err, "setting up the database")
	}

	defer func() {
		if closeErr := botDB.Close(); closeErr != nil {
			if err != nil {
				err = errors.Wrap(err, closeErr.Error())
			} else {
				err = closeErr
			}
		}
	}()
	// Authentication flow handles authentication process, like prompting for code and 2FA password.

	flow := auth.NewFlow(Terminal{PhoneNumber: options.Env.Phone}, auth.SendCodeOptions{})

	bot := botWrapper.New(ctx, api)

	watcherOptions := monitor.Options{
		Thresholds: monitor.Thresholds(options.Env.Thresholds),
		Chats: monitor.Chats{
			Sources:      options.ChatsToMonitor,
			Destinations: options.DestChannels,
		},
		NoQuoteWhitelist: options.NoQuoteWhitelist,
	}

	watcher := monitor.New(watcherOptions, botDB, bot, logger)

	// Registering handler for new private messages in chats.
	dispatcher.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewChannelMessage) error {
		handlerCtx := HandlerContext{ctx: ctx, e: e, u: u, peerDB: peerDB, botDB: botDB, watcher: watcher}
		childLogger := logger.Named("channel_message_handler")
		err := ChannelMessageHandler(handlerCtx, options, childLogger)
		if err != nil {
			logger.Errorw("Error in channel message handler", "error", err)
		}
		return err
	})

	return waiter.Run(ctx, func(ctx context.Context) error {
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
			logger.Info("Current user:", name)

			lg.Info("Login",
				zap.String("first_name", self.FirstName),
				zap.String("last_name", self.LastName),
				zap.String("username", self.Username),
				zap.Int64("id", self.ID),
			)

			if options.Env.Recover {
				// Handling missed messages
				err = watcher.RecoverSync()
				if err != nil {
					return errors.Wrap(err, "recovering missed messages")
				}
			}

			startMonitoring(ctx, watcher, options)

			return updatesRecovery.Run(ctx, api, self.ID, updates.AuthOptions{
				IsBot: self.Bot,
				OnStart: func(ctx context.Context) {
					logger.Info("Update recovery initialized and started, listening for events")
				},
			})
		}); err != nil {
			return errors.Wrap(err, "run")
		}

		return nil
	})
}

func startMonitoring(ctx context.Context, watcher *monitor.Monitor, options Options) {
	// Start monitoring wide range
	wideFrequency := time.Duration(options.Env.CheckFrequency.Wide) * time.Minute
	wideRange := time.Duration(options.Env.CheckRange.Wide) * time.Hour
	watcher.WatchSync(ctx, wideFrequency, wideRange)

	// Start monitoring narrow range
	narrowFrequency := time.Duration(options.Env.CheckFrequency.Narrow) * time.Minute
	narrowRange := time.Duration(options.Env.CheckRange.Narrow) * time.Hour
	watcher.WatchSync(ctx, narrowFrequency, narrowRange)
}

func prepareOptions() (options Options, err error) {
	var environment Environment
	_, err = env.UnmarshalFromEnviron(&environment)
	if err != nil {
		return options, errors.Wrap(err, "unmarshalling environment")
	}
	options.Env = environment

	for _, chatID := range environment.WatchedChatIDs {
		channel := tg.InputPeerChannel{
			ChannelID: chatID,
		}
		options.ChatsToMonitor = append(options.ChatsToMonitor, channel)
	}

	for _, chatID := range environment.NoQuoteWhitelistIDs {
		options.NoQuoteWhitelist = append(options.NoQuoteWhitelist, chatID)
	}

	if len(environment.DestChannelIDs) != len(environment.DestChannelAccessHashes) {
		return options, errors.New("dest channel id and access hash should have the same length")
	}
	for i, channelID := range environment.DestChannelIDs {
		channel := tg.InputPeerChannel{
			ChannelID:  channelID,
			AccessHash: environment.DestChannelAccessHashes[i],
		}
		options.DestChannels = append(options.DestChannels, &channel)
	}

	return options, nil
}

func main() {
	prodLogger, err := zap.NewDevelopment()
	logger := prodLogger.Sugar()
	if err != nil {
		log.Fatal(err)
	}

	options, err := prepareOptions()
	if err != nil {
		logger.Fatal(err)
	}

	ctx := context.Background()

	if err := run(ctx, options, logger); err != nil {
		logger.Fatal("Error", zap.Error(err))
	}
}
