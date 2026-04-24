# AGENTS.md

## Purpose

This file is the contributor and coding-agent guide for the `weibo-ai-bridge` repository.

- Use this document for repository structure, development workflow, testing, and change guardrails.
- Use `agents.md` for runtime AI agent access/configuration details exposed by the product itself.

## Project Summary

`weibo-ai-bridge` is a Go service that connects Weibo direct messages to local AI agent CLIs.

Current supported agent backends:

- Claude Code, registered as `claude-code`, exposed to sessions as `claude`
- Codex CLI, exposed as `codex`

At runtime the service:

1. Connects to the Weibo Open Platform WebSocket API.
2. Creates or resumes per-user sessions.
3. Routes commands and normal chat messages.
4. Streams agent output back to Weibo in chunks.
5. Exposes HTTP endpoints for health, stats, and SSE debugging.

## Repository Map

- `cmd/server/main.go`
  Service entrypoint, HTTP server, platform startup/shutdown, top-level message processing queue.
- `router/`
  Message routing, slash commands, interactive-session handling, stream forwarding, approval flow, `/btw` insertion.
- `agent/`
  Agent abstraction and concrete integrations for Claude and Codex, including interactive sessions and Codex app-server streaming.
- `platform/weibo/`
  Weibo platform adapter, message transport, chunked reply sender, request/response types.
- `session/`
  In-memory session manager plus optional persistence hooks.
- `config/`
  TOML/env config loading and validation.
- `deploy/`
  Deployment assets such as the `systemd` unit.
- `docs/`
  Design/spec notes and planning artifacts.
- `README.md`
  User-facing project overview and operational setup.
- `agents.md`
  Runtime AI-agent usage/configuration notes, not repo contribution guidance.

## Core Runtime Behaviors

These behaviors are important and should not be changed casually.

- Default HTTP port is `5533` unless `SERVER_PORT` is set.
- Config file path defaults to `config/config.toml` and can be overridden by `CONFIG_PATH`.
- At least one agent must be enabled or the server exits during config validation/startup.
- Session agent types are normalized at the router layer as `claude` or `codex`.
- Agent manager internally registers Claude under `claude-code` and resolves `claude` to `claude-code`.
- New sessions auto-increment by user as `<userID>-<n>`.
- Incoming non-command messages create or reuse the active session path; command messages are handled by `router/command.go`.
- `/btw` is special: it injects follow-up input into a live interactive session instead of behaving like a normal command.
- Codex prefers `codex app-server` streaming and falls back to JSON CLI execution when needed.
- Long replies are chunked with Chinese-safe rune boundaries and formatting-aware flush behavior.

## Commands And Interfaces

User-visible slash commands currently handled in `router/command.go`:

- `/help`
- `/new [claude|codex]`
- `/list`
- `/switch [index|claude|codex]`
- `/model`
- `/dir`
- `/status`
- `/btw <content>`

HTTP endpoints in `cmd/server/main.go`:

- `GET /health`
- `GET /stats`
- `GET /chat/stream`
- `POST /chat/stream`

When changing command semantics or endpoint payloads, update tests first and keep `README.md` aligned.

## Development Workflow

Common commands:

```bash
make build
make test
make fmt
make lint
make dev
```

Build output:

- `build/weibo-ai-bridge`

Notes:

- `make test` runs `go test -v -race -coverprofile=coverage.out ./...`
- `make fmt` applies `gofmt -w -s .`
- `make lint` expects `golangci-lint`
- A prebuilt `server` binary may exist in the repo root, but source changes should be validated against a fresh build

## Testing Expectations

Prefer the narrowest test scope that proves the change, then expand if the change crosses package boundaries.

Examples:

```bash
go test ./router ./agent
go test ./cmd/server
go test ./...
```

For changes affecting message flow, prioritize:

- `router/*_test.go`
- `agent/*_test.go`
- `cmd/server/main_test.go`
- `platform/weibo/*_test.go`
- `session/*_test.go`

If you change:

- command parsing, update `router/command_test.go`
- streaming/event translation, update `agent/*_test.go` and `router/router_test.go`
- HTTP handlers, update `cmd/server/main_test.go`
- config behavior, update `config/*_test.go`
- session lifecycle, update `session/session_test.go`

## Change Guardrails

- Keep user-facing Chinese copy consistent with existing command/status text unless the task explicitly asks for wording changes.
- Do not silently rename `claude-code` or `codex` identifiers without checking manager resolution and tests.
- Preserve stream event ordering guarantees: deltas/messages/errors/done are consumed by router and HTTP streaming paths.
- Be careful with session context keys such as `claude_session_id` and `codex_session_id`; they are part of resume behavior.
- Avoid introducing byte-based slicing for message chunks; use rune-safe behavior for Chinese output.
- Keep command handling and normal chat handling separate. `/btw` is the only slash command that participates in live conversation injection.
- Preserve graceful shutdown behavior in `cmd/server/main.go`.
- Do not assume Codex is always available through JSON mode only; app-server support is first-class in this codebase.

## Config Notes

Config is loaded from TOML first, then overridden by environment variables.

Common env vars:

- `CONFIG_PATH`
- `SERVER_PORT`
- `WEIBO_APP_ID`
- `WEIBO_APP_SECRET`
- `WEIBO_TOKEN_URL`
- `WEIBO_WS_URL`
- `CLAUDE_ENABLED`
- `CODEX_API_KEY`
- `CODEX_MODEL`
- `CODEX_ENABLED`
- `SESSION_TIMEOUT`
- `SESSION_MAX_SIZE`
- `LOG_LEVEL`
- `LOG_FORMAT`
- `LOG_OUTPUT`

Claude authentication is handled primarily by the local CLI environment. Codex may also rely on local CLI/provider configuration.

## Suggested Editing Strategy

When implementing changes:

1. Identify the layer first: `config`, `session`, `agent`, `router`, `platform`, or `cmd/server`.
2. Read the matching tests before editing behavior-heavy code.
3. Change the smallest layer that can own the behavior.
4. Add or update tests in the same package.
5. Run targeted tests, then broader tests if the change crosses layers.

## Known Repository Quirks

- The module path still uses `github.com/yourusername/weibo-ai-bridge`; do not "fix" it unless the task is explicitly about module renaming.
- The repo may contain generated or prebuilt artifacts such as the root `server` binary and `build/` outputs.
- `agents.md` already exists in lowercase. Keep it unless the task explicitly asks to merge or remove it.

## Documentation Maintenance

Update docs when behavior changes:

- `README.md` for operator-facing setup or endpoint/command changes
- `agents.md` for runtime AI backend configuration or availability changes
- `AGENTS.md` for repository workflow, architecture, or contribution expectations
