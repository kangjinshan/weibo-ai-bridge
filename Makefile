.PHONY: all build test clean fmt lint check-root-executables

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=gofmt

# Binary name
BINARY_NAME=weibo-ai-bridge
BINARY_UNIX=$(BINARY_NAME)_unix

# Main package
MAIN_PACKAGE=./cmd/server

# Build directory
BUILD_DIR=./build

# Build metadata
VERSION ?= dev
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -X 'main.version=$(VERSION)' -X 'main.gitCommit=$(GIT_COMMIT)' -X 'main.buildTime=$(BUILD_TIME)'

all: test build

check-root-executables:
	@bash ./scripts/check-root-executables.sh

build:
	@echo "Building..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PACKAGE)

test:
	@echo "Running tests..."
	@$(MAKE) check-root-executables
	$(GOTEST) -v -race -coverprofile=coverage.out ./...

test-coverage: test
	@echo "Generating coverage report..."
	$(GOCMD) tool cover -html=coverage.out -o coverage.html

clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

fmt:
	@echo "Formatting code..."
	$(GOFMT) -w -s .

lint:
	@echo "Linting..."
	@which golangci-lint > /dev/null || (echo "golangci-lint not found, please install it" && exit 1)
	golangci-lint run ./...

deps:
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) verify

tidy:
	@echo "Tidying dependencies..."
	$(GOMOD) tidy

# Development targets
dev: build
	@echo "Running in development mode..."
	./$(BUILD_DIR)/$(BINARY_NAME)

# Cross compilation
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_UNIX) $(MAIN_PACKAGE)

# Help
help:
	@echo "Available targets:"
	@echo "  all          - Run tests and build"
	@echo "  build        - Build the binary"
	@echo "  test         - Run tests with coverage"
	@echo "  test-coverage- Generate HTML coverage report"
	@echo "  clean        - Remove build artifacts"
	@echo "  fmt          - Format code"
	@echo "  lint         - Run linter"
	@echo "  deps         - Download dependencies"
	@echo "  tidy         - Tidy dependencies"
	@echo "  dev          - Build and run in dev mode"
	@echo "  build-linux  - Build for Linux AMD64"
