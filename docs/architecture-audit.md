# weibo-ai-bridge Structure Audit

Date: 2026-05-12

This audit captures the current package boundaries, maintenance risks, and a staged split order for future refactoring. It intentionally does not prescribe behavior changes; the first goal is to improve test protection and then split files where the boundaries are already visible.

## Current Package Boundaries

| Package | Responsibility | Current Boundary Notes |
| --- | --- | --- |
| `cmd/server` | Process bootstrap, HTTP handlers, message queueing, `/btw` busy-path injection, graceful shutdown. | The package owns runtime composition and message dispatch policy. HTTP handlers and queueing logic are currently in `main.go`, which makes focused testing harder. |
| `config` | TOML loading, environment overrides, defaults, validation. | This package is cohesive and has the strongest coverage. Keep it stable and use it as the model for small focused tests. |
| `session` | Session CRUD, active-session pointers, context accessors, persistence, legacy migration, native-ID adoption. | The public API is clear, but persistence and context mutation live in the same large file as simple CRUD. |
| `agent` | Claude, Codex, and Hermes CLI/ACP adapters plus common event interfaces. | The interface boundary is good. Individual adapters mix process lifecycle, protocol parsing, approval mapping, and stream translation. |
| `router` | Weibo message routing, slash commands, interactive session lifecycle, native session discovery, streaming output shaping, Super mode. | This is the most behavior-rich package. It has many good tests, but large files and mixed responsibilities make changes expensive. |
| `platform/weibo` | Weibo WebSocket client, token refresh, message parsing, deduplication, reply/chunk sending. | Transport and protocol framing are both here. Tests cover parsing and some send behavior, but stream sender integration remains thin. |
| `skills/weibo-skill-api` | Runtime skill scripts and references for Weibo API actions outside the Go service. | This is operationally related but not part of the Go bridge request/response path. Keep script compatibility changes separate from Go service changes. |

## Risk Files

| File | Lines | Risk | Why It Matters |
| --- | ---: | --- | --- |
| `router/router_test.go` | 3172 | Test maintainability | Many unrelated router behaviors share fixtures and mocks; failures are harder to localize. |
| `router/command_test.go` | 1338 | Test maintainability | Command tests are comprehensive but dense. New commands increase setup noise. |
| `session/session.go` | 1213 | Mixed responsibilities | CRUD, locking, persistence, migration, context cloning, and native-ID merging are coupled. |
| `router/command.go` | 1074 | Mixed responsibilities | Command parsing, native session presentation, path resolution, and repair hooks live together. |
| `router/native_sessions.go` | 1076 | Adapter coupling | Claude, Codex, and Hermes native session scans share one file even though formats and data sources differ. |
| `agent/hermes.go` | 918 | Protocol complexity | ACP JSON-RPC lifecycle, approval flow, replay suppression, and CLI fallback parsing are mixed. |
| `cmd/server/main.go` | 769 | Composition hotspot | Bootstrap, HTTP handlers, queueing, and log setup share one file. |
| `platform/weibo/client.go` | 681 | Transport complexity | WebSocket lifecycle, heartbeat, reconnect, chunk framing, and deduplication are tightly grouped. |

## Coverage Baseline

Baseline command:

```bash
go test -coverprofile=/tmp/weibo-ai-bridge-coverage.out ./...
```

Observed package coverage:

| Package | Coverage |
| --- | ---: |
| `config` | 84.6% |
| `session` | 72.8% |
| `router` | 69.8% |
| `platform/weibo` | 58.9% |
| `agent` | 54.7% |
| `cmd/server` | 45.1% |

The first coverage goal should not be a global percentage target. The healthier target is to cover high-risk low-coverage functions in `cmd/server`, `session`, `router/stream_sender`, and pure protocol parsing helpers under `agent`.

## Recommended Split Order

1. **Add reporting before refactoring.** Keep `make test` unchanged and add `make test-report` so every future refactor has readable package coverage, low-coverage function lists, and failure summaries.
2. **Split tests before production files.** Move router fixtures and stream sender tests into focused files such as `router/test_fixtures_test.go`, `router/stream_sender_test.go`, and `router/super_mode_test.go`. This reduces friction without behavior risk.
3. **Extract `cmd/server` queueing.** Move `messageProcessor` and related interfaces from `cmd/server/main.go` to a dedicated file in the same package, then later consider a small internal package only if reuse appears.
4. **Split session persistence.** Keep `Session` and `Manager` APIs stable, but move persistence, metadata, legacy import, and atomic write helpers into focused files inside `session`.
5. **Split native session discovery by agent.** Move Claude, Codex, and Hermes native scanners into separate router files while keeping the public `NativeSession` type and list functions stable.
6. **Split agent protocol helpers.** For Hermes and Codex, separate pure parsing/approval helpers from process and WebSocket lifecycle code. Pure helpers should remain easy to unit test without spawning CLIs.
7. **Split Weibo transport from framing.** Keep `Platform` as the public entry point, but isolate frame building, chunk sending, and message parsing tests so transport changes do not disturb protocol fixtures.

## Testing Priorities

1. Add tests around `cmd/server` HTTP stats and log formatting because they are user-facing and currently under-covered.
2. Add tests around `session` atomic context mutation and persistence because session context now carries native Agent IDs and Super mode state.
3. Add tests around `router/stream_sender` partial snapshots and finalization because chunk ordering directly affects Weibo replies.
4. Add tests around Claude/Codex/Hermes pure parsers before splitting their files.
5. Keep integration-style router tests, but gradually group them by behavior so future failures point to a narrower area.

## Non-Goals For The First Pass

- Do not change command semantics.
- Do not rename Agent identifiers such as `claude-code`, `codex`, or `hermes`.
- Do not move session context keys.
- Do not replace the Codex app-server path or Hermes ACP path.
- Do not rewrite WebSocket behavior while adding report tooling.
