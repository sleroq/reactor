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
	for part := range strings.SplitSeq(value, ",") {
		number, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			return err
		}
		*i = append(*i, number)
	}

	return nil
}

type Environment struct {
	Phone           string `env:"REACTOR_PHONE,required=true"`
	AppID           int    `env:"REACTOR_APP_ID,required=true"`
	AppHash         string `env:"REACTOR_APP_HASH,required=true"`
	CommandBotToken string `env:"REACTOR_COMMAND_BOT_TOKEN,required=true"`
	SessionDir      string `env:"REACTOR_SESSION_DIR,required=true"`

	WatchedChatIDs          Int64Slice `env:"REACTOR_CHAT_IDS,required=true"`
	DestChannelIDs          Int64Slice `env:"REACTOR_CHANNEL_ID,required=true"`
	DestChannelAccessHashes Int64Slice `env:"REACTOR_CHANNEL_ACCESS_HASH,required=true"`

	NoQuoteWhitelistIDs Int64Slice `env:"REACTOR_NOQUOTE_WHITELIST"`

	Thresholds struct {
		Text       int `env:"REACTOR_TEXT_THRESHOLD,default=31"`
		Photo      int `env:"REACTOR_PHOTO_THRESHOLD,default=23"`
		Forward    int `env:"REACTOR_FORWARD_THRESHOLD,default=23"`
		TextMax    int `env:"REACTOR_TEXT_MAX_THRESHOLD,default=62"`
		PhotoMax   int `env:"REACTOR_PHOTO_MAX_THRESHOLD,default=46"`
		ForwardMax int `env:"REACTOR_FORWARD_MAX_THRESHOLD,default=46"`
	}
	ThresholdHistoryDays   int `env:"REACTOR_THRESHOLD_HISTORY_DAYS,default=7"`
	ThresholdMaturityHours int `env:"REACTOR_THRESHOLD_MATURITY_HOURS,default=6"`
	TargetForwardsPerDay   int `env:"REACTOR_TARGET_FORWARDS_PER_DAY,default=3"`

	CheckFrequency struct {
		Wide   int `env:"REACTOR_WIDE_FREQUENCY,default=180"`
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

type TelegramRuntime struct {
	client          *telegram.Client
	api             *tg.Client
	peerDB          *pebble.PeerStorage
	updatesRecovery *updates.Manager
	waiter          *floodwait.Waiter
}

func prepareTelegramRuntime(
	appID int,
	appHash string,
	sessionDir string,
	logger *zap.SugaredLogger,
	lg *zap.Logger,
	dispatcher tg.UpdateDispatcher,
) (*TelegramRuntime, error) {
	sessionStorage := &telegram.FileSessionStorage{
		Path: filepath.Join(sessionDir, "session.json"),
	}
	cacheDB, err := pebbledb.Open(filepath.Join(sessionDir, "peers.pebble.db"), &pebbledb.Options{})
	if err != nil {
		return nil, errors.Wrap(err, "create pebble storage")
	}
	peerDB := pebble.NewPeerStorage(cacheDB)
	lg.Info("Storage", zap.String("path", sessionDir))

	updateHandler := storage.UpdateHook(dispatcher, peerDB)

	boltdb, err := bbolt.Open(filepath.Join(sessionDir, "updates.bolt.db"), 0666, nil)
	if err != nil {
		return nil, errors.Wrap(err, "create bolt storage")
	}
	updatesRecovery := updates.New(updates.Config{
		Handler: updateHandler,
		Logger:  lg.Named("updates.recovery"),
		Storage: boltstor.NewStateStorage(boltdb),
	})

	waiter := floodwait.NewWaiter().WithCallback(func(ctx context.Context, wait floodwait.FloodWait) {
		lg.Warn("Flood wait", zap.Duration("wait", wait.Duration))
		logger.Warn("Flood wait", zap.Duration("wait", wait.Duration))
	})

	clientOptions := telegram.Options{
		Logger:         lg,
		SessionStorage: sessionStorage,
		UpdateHandler:  updatesRecovery,
		Middlewares: []telegram.Middleware{
			waiter,
			ratelimit.New(rate.Every(time.Millisecond*500), 5),
		},
	}

	client := telegram.NewClient(appID, appHash, clientOptions)
	api := client.API()
	_ = storage.NewResolverCache(peer.Plain(api), peerDB)

	return &TelegramRuntime{
		client:          client,
		api:             api,
		peerDB:          peerDB,
		updatesRecovery: updatesRecovery,
		waiter:          waiter,
	}, nil
}

func run(ctx context.Context, options Options, logger *zap.SugaredLogger) (err error) {
	userSessionDir := filepath.Join(options.Env.SessionDir, sessionFolder(options.Env.Phone))
	if err := os.MkdirAll(userSessionDir, 0700); err != nil {
		return errors.Wrap(err, "create userbot session dir")
	}

	commandSessionDir := filepath.Join(options.Env.SessionDir, "command-bot")
	if err := os.MkdirAll(commandSessionDir, 0700); err != nil {
		return errors.Wrap(err, "create command bot session dir")
	}

	userInternalLogger := prepareInternalLogger(userSessionDir)
	defer func() { _ = userInternalLogger.Sync() }()

	commandInternalLogger := prepareInternalLogger(commandSessionDir)
	defer func() { _ = commandInternalLogger.Sync() }()

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

	userDispatcher := tg.NewUpdateDispatcher()
	userRuntime, err := prepareTelegramRuntime(
		options.Env.AppID,
		options.Env.AppHash,
		userSessionDir,
		logger,
		userInternalLogger,
		userDispatcher,
	)
	if err != nil {
		return errors.Wrap(err, "preparing userbot runtime")
	}

	userBot := botWrapper.New(ctx, userRuntime.api)
	watcherOptions := monitor.Options{
		Thresholds:             monitor.Thresholds(options.Env.Thresholds),
		ThresholdHistoryWindow: time.Duration(options.Env.ThresholdHistoryDays) * 24 * time.Hour,
		ThresholdMaturityAge:   time.Duration(options.Env.ThresholdMaturityHours) * time.Hour,
		TargetForwardsPerDay:   options.Env.TargetForwardsPerDay,
		Chats: monitor.Chats{
			Sources:      options.ChatsToMonitor,
			Destinations: options.DestChannels,
		},
		NoQuoteWhitelist: options.NoQuoteWhitelist,
	}
	watcher := monitor.New(watcherOptions, botDB, userBot, logger)

	userDispatcher.OnNewChannelMessage(func(ctx context.Context, _ tg.Entities, u *tg.UpdateNewChannelMessage) error {
		handlerCtx := HandlerContext{ctx: ctx, u: u, peerDB: userRuntime.peerDB, botDB: botDB, watcher: watcher}
		childLogger := logger.Named("channel_message_handler")
		err := ChannelMessageHandler(handlerCtx, options, childLogger)
		if err != nil {
			logger.Errorw("Error in channel message handler", "error", err)
		}
		return err
	})

	commandDispatcher := tg.NewUpdateDispatcher()
	commandRuntime, err := prepareTelegramRuntime(
		options.Env.AppID,
		options.Env.AppHash,
		commandSessionDir,
		logger,
		commandInternalLogger,
		commandDispatcher,
	)
	if err != nil {
		return errors.Wrap(err, "preparing command bot runtime")
	}

	commandBot := botWrapper.New(ctx, commandRuntime.api)

	errChan := make(chan error, 2)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		errChan <- runMonitoringClient(runCtx, userRuntime, watcher, options, userInternalLogger, logger)
	}()

	go func() {
		errChan <- runCommandClient(runCtx, commandRuntime, commandDispatcher, watcher, commandBot, options, commandInternalLogger, logger)
	}()

	var firstErr error
	for range 2 {
		runErr := <-errChan
		if runErr != nil && firstErr == nil {
			firstErr = runErr
			cancel()
		}
	}

	return firstErr
}

func runMonitoringClient(
	ctx context.Context,
	runtime *TelegramRuntime,
	watcher *monitor.Monitor,
	options Options,
	lg *zap.Logger,
	logger *zap.SugaredLogger,
) error {
	flow := auth.NewFlow(Terminal{PhoneNumber: options.Env.Phone}, auth.SendCodeOptions{})

	return runtime.waiter.Run(ctx, func(ctx context.Context) error {
		if err := runtime.client.Run(ctx, func(ctx context.Context) error {
			if err := runtime.client.Auth().IfNecessary(ctx, flow); err != nil {
				return errors.Wrap(err, "auth")
			}

			self, err := runtime.client.Self(ctx)
			if err != nil {
				return errors.Wrap(err, "call self")
			}

			name := self.FirstName
			if self.Username != "" {
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
				err = watcher.RecoverSync()
				if err != nil {
					return errors.Wrap(err, "recovering missed messages")
				}
			}

			startMonitoring(ctx, watcher, options)

			return runtime.updatesRecovery.Run(ctx, runtime.api, self.ID, updates.AuthOptions{
				IsBot: self.Bot,
				OnStart: func(ctx context.Context) {
					logger.Info("Update recovery initialized and started, listening for events")
				},
			})
		}); err != nil {
			return errors.Wrap(err, "running monitoring client")
		}

		return nil
	})
}

func runCommandClient(
	ctx context.Context,
	runtime *TelegramRuntime,
	dispatcher tg.UpdateDispatcher,
	watcher *monitor.Monitor,
	commandBot *botWrapper.Bot,
	options Options,
	lg *zap.Logger,
	logger *zap.SugaredLogger,
) error {
	return runtime.waiter.Run(ctx, func(ctx context.Context) error {
		if err := runtime.client.Run(ctx, func(ctx context.Context) error {
			status, err := runtime.client.Auth().Status(ctx)
			if err != nil {
				return errors.Wrap(err, "checking command bot auth status")
			}

			if !status.Authorized {
				if _, err := runtime.client.Auth().Bot(ctx, options.Env.CommandBotToken); err != nil {
					return errors.Wrap(err, "authorizing command bot")
				}
			}

			self, err := runtime.client.Self(ctx)
			if err != nil {
				return errors.Wrap(err, "getting command bot self")
			}
			tokenBotID, err := commandBotID(options.Env.CommandBotToken)
			if err != nil {
				return errors.Wrap(err, "parsing command bot token")
			}
			if self.ID != tokenBotID {
				return fmt.Errorf(
					"command bot session belongs to @%s (%d), but configured token belongs to bot %d; remove %s and restart",
					self.Username,
					self.ID,
					tokenBotID,
					filepath.Join(options.Env.SessionDir, "command-bot"),
				)
			}

			lg.Info("Login",
				zap.String("first_name", self.FirstName),
				zap.String("last_name", self.LastName),
				zap.String("username", self.Username),
				zap.Int64("id", self.ID),
			)
			logger.Infow("Command bot connected", "username", self.Username, "id", self.ID)
			registerCommandHandlers(dispatcher, runtime.peerDB, watcher, commandBot, self.Username, options, logger)

			return runtime.updatesRecovery.Run(ctx, runtime.api, self.ID, updates.AuthOptions{
				IsBot: true,
				OnStart: func(ctx context.Context) {
					logger.Info("Command bot update recovery initialized and started")
				},
			})
		}); err != nil {
			return errors.Wrap(err, "running command bot client")
		}

		return nil
	})
}

func registerCommandHandlers(
	dispatcher tg.UpdateDispatcher,
	peerDB *pebble.PeerStorage,
	watcher *monitor.Monitor,
	commandBot *botWrapper.Bot,
	username string,
	options Options,
	logger *zap.SugaredLogger,
) {
	handler := func(ctx context.Context, msg tg.MessageClass) error {
		handlerCtx := CommandHandlerContext{
			ctx:      ctx,
			u:        msg,
			peerDB:   peerDB,
			watcher:  watcher,
			bot:      commandBot,
			username: username,
		}

		err := CommandMessageHandler(handlerCtx, options, logger.Named("command_message_handler"))
		if err != nil {
			logger.Errorw("Error in command message handler", "error", err)
		}

		return err
	}

	dispatcher.OnNewMessage(func(ctx context.Context, _ tg.Entities, u *tg.UpdateNewMessage) error {
		return handler(ctx, u.Message)
	})

	dispatcher.OnNewChannelMessage(func(ctx context.Context, _ tg.Entities, u *tg.UpdateNewChannelMessage) error {
		return handler(ctx, u.Message)
	})
}

func commandBotID(token string) (int64, error) {
	id, _, ok := strings.Cut(token, ":")
	if !ok {
		return 0, errors.New("token has no separator")
	}

	botID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0, errors.Wrap(err, "parsing bot ID")
	}
	return botID, nil
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
	if err := validateThresholds("text", environment.Thresholds.Text, environment.Thresholds.TextMax); err != nil {
		return options, err
	}
	if err := validateThresholds("photo", environment.Thresholds.Photo, environment.Thresholds.PhotoMax); err != nil {
		return options, err
	}
	if err := validateThresholds("forward", environment.Thresholds.Forward, environment.Thresholds.ForwardMax); err != nil {
		return options, err
	}
	if environment.ThresholdHistoryDays <= 0 {
		return options, errors.New("threshold history days must be positive")
	}
	if environment.ThresholdMaturityHours <= 0 {
		return options, errors.New("threshold maturity hours must be positive")
	}
	if environment.TargetForwardsPerDay <= 0 {
		return options, errors.New("target forwards per day must be positive")
	}

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

func validateThresholds(name string, minimum, maximum int) error {
	if minimum < 0 || maximum < 0 {
		return fmt.Errorf("%s thresholds must be non-negative", name)
	}
	if minimum > maximum {
		return fmt.Errorf("%s minimum threshold must not exceed maximum threshold", name)
	}

	return nil
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
