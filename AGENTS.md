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
- The app uses phone authentication (not the Bot API).
- Keep this file limited to non-obvious, durable project constraints.
