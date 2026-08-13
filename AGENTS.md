# Reactor

Go Telegram userbot that forwards popular chat messages. It uses SQLite, so
build with `CGO_ENABLED=1`.

```bash
nix develop
go test ./...
go fmt ./...
golangci-lint run --enable=modernize
```

- Follow nearby code and standard Go conventions.
- Wrap errors with `github.com/go-faster/errors`.
- The monitoring client uses phone authentication; the command client uses a Bot API token.
- Keep this file limited to non-obvious, durable project constraints.
