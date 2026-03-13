# Makefile for agenthub

.PHONY: all deps build install test test-cover test-integration fmt lint setup clean help

# Default target
all: build

BUILD_DIR    := .
GIT_BUILD    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
VERSION      := $(shell cat VERSION 2>/dev/null || echo "0.0.0")
BINARY       := $(BUILD_DIR)/agenthub
INSTALL_DIR  ?= $(shell go env GOPATH)/bin

HTMX_VERSION := 2.0.4
HTMX_URL     := https://unpkg.com/htmx.org@$(HTMX_VERSION)/dist/htmx.min.js
HTMX_JS      := web/static/htmx.min.js

# Dolt backend (via beads) requires CGO.
export CGO_ENABLED := 1

# Match go.mod toolchain version
GO_VERSION := $(shell sed -n 's/^go //p' go.mod)
ifneq ($(GO_VERSION),)
export GOTOOLCHAIN := go$(GO_VERSION)
endif

# ICU4C is keg-only in Homebrew; Dolt's go-icu-regex needs these paths.
ifneq ($(OS),Windows_NT)
ICU_PREFIX := $(shell brew --prefix icu4c 2>/dev/null)
ifneq ($(ICU_PREFIX),)
export CGO_CFLAGS   += -I$(ICU_PREFIX)/include
export CGO_CPPFLAGS += -I$(ICU_PREFIX)/include
export CGO_LDFLAGS  += -L$(ICU_PREFIX)/lib
ifeq ($(shell uname),Linux)
export CXX ?= g++
endif
endif
endif

LDFLAGS := -X main.Version=$(VERSION) -X main.Build=$(GIT_BUILD)

# ── Dependency targets ────────────────────────────────────────────────────────

## deps: Install all system and Go dependencies (run once before first build)
deps: $(HTMX_JS)
ifeq ($(shell uname),Darwin)
	@echo "==> Checking macOS build dependencies..."
	@# Xcode Command Line Tools provide clang (required for CGO).
	@xcode-select -p >/dev/null 2>&1 || \
		(echo "Installing Xcode Command Line Tools (required for CGO)..." && \
		 xcode-select --install && \
		 echo "Re-run 'make deps' after the installer finishes." && exit 1)
	@which brew >/dev/null 2>&1 || \
		(echo "ERROR: Homebrew not found. Install from https://brew.sh" && exit 1)
	@which go >/dev/null 2>&1 || brew install go
	@brew list icu4c >/dev/null 2>&1 || brew install icu4c
	@which curl >/dev/null 2>&1 || brew install curl
	@which bc   >/dev/null 2>&1 || brew install bc
else ifeq ($(shell uname),Linux)
	@echo "==> Installing Linux build dependencies..."
	@if which apt-get >/dev/null 2>&1; then \
		sudo apt-get update -q && \
		sudo apt-get install -y \
			build-essential curl bc \
			libicu-dev pkg-config \
			golang-go; \
	elif which dnf >/dev/null 2>&1; then \
		sudo dnf install -y \
			gcc gcc-c++ curl bc \
			libicu-devel pkgconfig \
			golang; \
	elif which yum >/dev/null 2>&1; then \
		sudo yum install -y \
			gcc gcc-c++ curl bc \
			libicu-devel pkgconfig \
			golang; \
	else \
		echo "WARNING: Unknown package manager."; \
		echo "  Please install manually: Go, gcc/clang, libicu-dev, curl, bc"; \
	fi
endif
	@echo "==> Downloading Go module dependencies..."
	go mod download
	@echo "==> All dependencies installed."

# Download htmx only if the file is missing (file target — not re-downloaded on every build).
$(HTMX_JS):
	@echo "Downloading htmx $(HTMX_VERSION)..."
	@mkdir -p web/static
	curl -sSfL -o $@ "$(HTMX_URL)"

# ── Build targets ─────────────────────────────────────────────────────────────

## build: Compile the agenthub binary (downloads htmx if missing)
build: $(HTMX_JS)
	@echo "Building agenthub $(VERSION) ($(GIT_BUILD))..."
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) ./src/cmd/agenthub/...
ifeq ($(shell uname),Darwin)
	@codesign -s - -f $(BINARY) 2>/dev/null || true
endif

## install: Build and install agenthub to INSTALL_DIR (default: GOPATH/bin)
install: build
	@echo "Installing agenthub to $(INSTALL_DIR)..."
	@mkdir -p "$(INSTALL_DIR)"
	cp $(BINARY) "$(INSTALL_DIR)/agenthub"
	@echo "Installed: $(INSTALL_DIR)/agenthub"

# ── Test targets ──────────────────────────────────────────────────────────────

## test: Run all unit tests
test: $(HTMX_JS)
	@echo "Running tests..."
	go test ./src/... -timeout 120s

## test-cover: Run tests with coverage gate (minimum 90%)
test-cover: $(HTMX_JS)
	@echo "Running tests with coverage..."
	go test ./src/... -coverprofile=coverage.out -covermode=atomic -timeout 120s
	@go tool cover -func=coverage.out | tail -1
	@COVERAGE=$$(go tool cover -func=coverage.out | tail -1 | awk '{print $$3}' | tr -d '%'); \
	  echo "Total coverage: $$COVERAGE%"; \
	  if [ "$$(echo "$$COVERAGE < 90" | bc -l)" = "1" ]; then \
	    echo "ERROR: Coverage $$COVERAGE% is below 90% minimum"; \
	    exit 1; \
	  fi
	@echo "Coverage OK."

## test-integration: Run integration tests (requires running Dolt server)
test-integration:
	go test -tags integration ./tests/integration/... -v -timeout 300s

# ── Code quality ──────────────────────────────────────────────────────────────

## fmt: Format all Go source files
fmt:
	gofmt -w ./src/...

## lint: Run static analysis
lint:
	go vet ./src/...

# ── Runtime targets ───────────────────────────────────────────────────────────

## setup: First-run: initialize admin password and encrypted store
setup:
	@echo "Running first-time setup..."
	go run ./src/cmd/agenthub/... setup

# ── Housekeeping ──────────────────────────────────────────────────────────────

## clean: Remove build artifacts (keeps downloaded deps)
clean:
	rm -f $(BINARY) coverage.out

## clean-deps: Remove downloaded dependency files (htmx, etc.)
clean-deps:
	rm -f $(HTMX_JS)

## help: Show available targets
help:
	@grep -E '^##' Makefile | sed 's/^## //'
