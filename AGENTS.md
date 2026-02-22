# AGENTS.md - Reactor Codebase Guide

Guidance for AI coding agents working in this Telegram user-bot repository.

## Project Overview

- **Language**: Go 1.25.0 | **Module**: `github.com/sleroq/reactor`
- **Database**: SQLite (mattn/go-sqlite3) | **Session Storage**: Pebble + BoltDB
- **Purpose**: Monitors chats for popular messages (by reactions/replies) and forwards them

## Commands

```bash
# Development
nix develop                    # Enter dev environment
go build -o dist/reactor src/*.go
go run src/main.go

# Testing
go test ./...                  # All tests
go test ./src/db/...           # Package tests
go test -run TestFunctionName ./src/package/  # Single test
go test -v ./...               # Verbose
go test -cover ./...           # Coverage

# Linting & Formatting
golangci-lint run
go fmt ./...
go vet ./...

# Dependencies
go mod tidy
go mod download
```

## Project Structure

```
src/
├── main.go          # Entry point, client setup
├── handlers.go      # Telegram update handlers
├── terminal.go      # CLI auth helpers
├── bot/bot.go       # Telegram API wrapper
├── db/db.go         # SQLite operations, models
├── monitor/monitor.go # Message monitoring logic
└── helpers/helpers.go  # Utilities
```

## Code Style

### Imports

Group in three sections (stdlib, external, internal):

```go
import (
    "context"
    "fmt"

    "github.com/go-faster/errors"
    "go.uber.org/zap"

    "github.com/sleroq/reactor/src/db"
)
```

### Error Handling

Use `github.com/go-faster/errors` for wrapping:

```go
if err != nil {
    return errors.Wrap(err, "getting messages from database")
}
```

### Logging

Use `go.uber.org/zap` with SugaredLogger; create named child loggers:

```go
logger := parentLogger.Named("monitor")
logger.Errorw("Error in handler", "error", err)
```

### Naming

- **Packages**: lowercase, single word (`bot`, `db`, `monitor`)
- **Types/Exports**: PascalCase (`Message`, `Monitor`)
- **Unexported**: camelCase
- **Acronyms**: consistent (`ID`, `API`)

### Structs & Constructors

```go
type Monitor struct {
    db      *sql.DB
    bot     *bot.Bot
    options Options
    mu      *sync.Mutex
}

func New(options Options, db *sql.DB, bot *bot.Bot) *Monitor {
    return &Monitor{db, bot, options, &sync.Mutex{}}
}
```

### Type Switches

Handle Telegram union types:

```go
switch v := msg.FromID.(type) {
case *tg.PeerUser:
    userId = v.UserID
case *tg.PeerChannel:
    // handle
default:
    return fmt.Errorf("unexpected type: %T", v)
}
```

### Context & Concurrency

- Pass `context.Context` as first parameter to blocking functions
- Use mutexes for shared state in goroutines

```go
func (m Monitor) checkMessagesSafely(ageLimit time.Duration) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    return m.checkForNewMessages(ageLimit)
}
```

### Database

Use named parameters and wrap errors:

```go
rows, err := db.Query(`select * from messages where chatId = :chatID`, chatID)
if err != nil {
    return nil, errors.Wrap(err, "getting messages")
}
```

## Environment Variables

Set in `scripts/env.bash`:

| Variable | Description |
|----------|-------------|
| `REACTOR_PHONE` | Telegram phone number |
| `REACTOR_APP_ID` | Telegram API ID |
| `REACTOR_APP_HASH` | Telegram API Hash |
| `REACTOR_SESSION_DIR` | Session directory (default: `./session`) |
| `REACTOR_CHAT_IDS` | Comma-separated chat IDs to monitor |
| `REACTOR_CHANNEL_ID` | Destination channel IDs |
| `REACTOR_CHANNEL_ACCESS_HASH` | Destination channel access hashes |

## Key Patterns

### Bot Token Auth (gotd)

For a dedicated bot account, use `client.Auth().Status(ctx)` and then `client.Auth().Bot(ctx, token)` if unauthorized. `auth.NewFlow(...)` is for user phone auth only.

### Flood Wait Handling

```go
waiter := floodwait.NewWaiter().WithCallback(func(ctx context.Context, wait floodwait.FloodWait) {
    lg.Warn("Flood wait", zap.Duration("wait", wait.Duration))
})
```

### Generics

```go
func Part[T any](slice []T, length int) (new []T, modified []T) {
    if length > len(slice) { length = len(slice) }
    return slice[:length], slice[length:]
}
```

## Important Notes

- **CGO_ENABLED=1** required (SQLite)
- Session data: `./session/` | Database: `./reactor.db`
- Runs as userbot (not bot API), requires phone auth

## A note to the agent

When you learn something non-obvious **that will help** future feature work, add it here.
