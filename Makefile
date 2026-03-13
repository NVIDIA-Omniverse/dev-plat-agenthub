# Makefile for agenthub

.PHONY: all build test test-cover fmt lint clean setup help

# Default target
all: build

BUILD_DIR := .
GIT_BUILD := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
VERSION    := $(shell cat VERSION 2>/dev/null || echo "0.0.0")
BINARY     := $(BUILD_DIR)/agenthub

# Dolt backend (via beads) requires CGO.
export CGO_ENABLED := 1

# Match go.mod toolchain version
GO_VERSION := $(shell sed -n 's/^go //p' go.mod)
ifneq ($(GO_VERSION),)
export GOTOOLCHAIN := go$(GO_VERSION)
endif

# ICU4C is keg-only in Homebrew; Dolt's go-icu-regex needs these paths.
# On Windows, ICU is not needed (pure-Go regex fallback).
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

## build: Compile the agenthub binary
build:
	@echo "Building agenthub $(VERSION) ($(GIT_BUILD))..."
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) ./src/cmd/agenthub/...
ifeq ($(shell uname),Darwin)
	@codesign -s - -f $(BINARY) 2>/dev/null || true
endif

## test: Run all unit tests
test:
	@echo "Running tests..."
	go test ./src/... -timeout 120s

## test-cover: Run tests with coverage gate (minimum 90%)
test-cover:
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

## fmt: Format all Go source files
fmt:
	gofmt -w ./src/...

## lint: Run static analysis
lint:
	go vet ./src/...

## setup: First-run: initialize admin password and encrypted store
setup:
	@echo "Running first-time setup..."
	go run ./src/cmd/agenthub/... setup

## clean: Remove build artifacts
clean:
	rm -f $(BINARY) coverage.out

## help: Show this help
help:
	@grep -E '^##' Makefile | sed 's/^## //'
