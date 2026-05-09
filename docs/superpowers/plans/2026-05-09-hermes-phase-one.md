# Hermes Phase One Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Hermes CLI as a third first-class Agent backend alongside Claude Code and Codex CLI.

**Architecture:** Hermes implements the existing `agent.Agent` and `agent.InteractiveAgent` interfaces. The main path starts `hermes acp` and speaks newline-delimited JSON-RPC (`initialize`, `session/new|resume`, `session/prompt`) so bridge can preserve native ACP `sessionId`, stream deltas, surface approval events, and map `/btw` onto ACP `/steer` during an active turn. The older `hermes chat --quiet --source tool --query` path remains as a CLI fallback.

**Tech Stack:** Go, local Hermes CLI ACP mode, existing router/session/config abstractions, `go test`.

---

### Task 1: Config And Registration

**Files:**
- Modify: `config/config.go`
- Modify: `config/config.toml`
- Modify: `config/config.example.toml`
- Modify: `cmd/server/main.go`
- Test: `config/config_test.go`
- Test: `config/config_load_test.go`
- Test: `config/config_validate_test.go`

- [ ] Write failing config tests for `[agent.hermes]`, `HERMES_ENABLED`, `HERMES_MODEL`, `HERMES_PROFILE`, and validation with only Hermes enabled.
- [ ] Run `go test ./config` and confirm the tests fail because Hermes config fields do not exist.
- [ ] Add `HermesConfig`, defaults, environment overrides, validation inclusion, and sample TOML entries.
- [ ] Register `agent.NewHermesAgent` in server startup when Hermes is enabled.
- [ ] Run `go test ./config ./cmd/server` and confirm green.

### Task 2: Hermes Agent CLI Adapter

**Files:**
- Create: `agent/hermes.go`
- Test: `agent/hermes_test.go`

- [ ] Write failing tests for Hermes command construction, session extraction, plain output parsing, and `Name() == "hermes"`.
- [ ] Run `go test ./agent -run Hermes` and confirm failure because Hermes code does not exist.
- [ ] Implement `HermesAgent` using `hermes chat --quiet --query <prompt> --source tool`, with optional `--model`, `--provider`, `--resume`, and `--profile`.
- [ ] Parse `Session:` or `Session ID:` lines into `EventTypeSession`, emit response text as `EventTypeMessage`, and finish with `EventTypeDone`.
- [ ] Run `go test ./agent -run Hermes` and confirm green.

### Task 3: Router Commands And Session Binding

**Files:**
- Modify: `agent/manager.go`
- Modify: `router/command.go`
- Modify: `router/router_agent.go`
- Modify: `router/router_session_binding.go`
- Test: `agent/manager_test.go`
- Test: `router/command_test.go`
- Test: `router/router_test.go`

- [ ] Write failing tests for `ResolveAgent("hermes")`, `/new hermes`, `/switch hermes`, `/hermes`, and `hermes_session_id`.
- [ ] Run targeted tests and confirm they fail on current Claude/Codex-only validation.
- [ ] Add Hermes agent candidate, command alias, allowed-agent helper, repair hook, session context key, and pending-session cleanup.
- [ ] Run `go test ./agent ./router` and confirm green.

### Task 4: Native Session Discovery And Workdir

**Files:**
- Modify: `router/native_sessions.go`
- Modify: `router/command.go`
- Test: `router/native_sessions_test.go`
- Test: `router/command_test.go`

- [ ] Write failing tests for parsing Hermes `session_*.json` files under a fake `HOME/.hermes/sessions`.
- [ ] Run `go test ./router -run 'Hermes|Native'` and confirm the Hermes tests fail.
- [ ] Implement `ListNativeHermesSessions`, include Hermes sessions in `/list`, switch-by-number, native ID lookup, and workdir recovery.
- [ ] Run `go test ./router` and confirm green.

### Task 5: Docs And Verification

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`

- [ ] Update user-facing docs for Hermes CLI requirements, config, commands, session ID behavior, and troubleshooting.
- [ ] Run `gofmt -w -s` on changed Go files.
- [ ] Run `go test ./agent ./config ./router ./cmd/server`.
- [ ] Run `make test` if the targeted suite is green.
