# Maintenance Test Report Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Document current package boundaries and add a readable test-report workflow while raising coverage in high-value, low-risk areas.

**Architecture:** Keep production behavior stable. Add a standalone Go test-report command under `cmd/test-report` so it can be unit tested and used by `make test-report`; add focused tests around existing pure parsing and helper logic before considering any large refactor.

**Tech Stack:** Go standard library, `go test -json`, `go tool cover -func`, Makefile targets, Markdown documentation.

---

### Task 1: Structure Audit Document

**Files:**
- Create: `docs/architecture-audit.md`

- [x] Record current packages, their responsibilities, and cross-package boundaries.
- [x] List risk files with concrete line counts and why each file is difficult to maintain.
- [x] Propose a staged split order that does not change behavior before test protection improves.

### Task 2: Test Report Tool

**Files:**
- Create: `cmd/test-report/main.go`
- Create: `cmd/test-report/main_test.go`
- Modify: `Makefile`

- [x] Write failing tests for parsing package coverage, low-coverage functions, failed test events, and report rendering.
- [x] Run `go test ./cmd/test-report` and confirm failure before implementation.
- [x] Implement `cmd/test-report` to run `go test -json -coverprofile`, parse the JSON stream and `go tool cover -func`, and write Markdown plus text reports.
- [x] Add `make test-report` and document generated files in the Makefile help.
- [x] Run `go test ./cmd/test-report` and `make test-report`.

### Task 3: First High-Value Tests

**Files:**
- Modify: `cmd/server/main_test.go`
- Modify: `session/session_test.go`
- Modify: `router/router_test.go`
- Modify: `agent/claude_test.go`
- Modify: `agent/codex_test.go`
- Modify: `agent/hermes_test.go`

- [x] Add focused tests for `statsHandler`, `jsonLogWriter`, and build-info fallback behavior.
- [x] Add focused tests for session atomic context mutation, boolean context parsing, and explicit persistence.
- [x] Add stream sender tests for partial snapshots, final delivery after partial deltas, informational flush, and legacy writer behavior.
- [x] Add parser tests for Claude content block starts, Codex tool events/message fallbacks, and Hermes JSON-RPC IDs/raw text/approval option selection.
- [x] Run targeted package tests after each group.

### Task 4: Verification

**Files:**
- Check generated reports under `reports/`

- [x] Run `gofmt -w -s` on changed Go files.
- [x] Run `go test ./cmd/test-report ./cmd/server ./session ./router ./agent`.
- [x] Run `make test-report`.
- [x] Run `make test` if targeted tests and report generation succeed.
