# Makefile for Sagittarius A* (aig)

BINARY     := aig
MODULE     := github.com/rhea/sagittarius-astar
CMD        := ./cmd/aig
BIN_DIR    := bin
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS    := -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: all build build-linux build-windows clean test vet fmt tidy run

all: build

## build: compile for the current host OS/arch
build:
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) $(CMD)
	@echo "✓ Built $(BIN_DIR)/$(BINARY)"

## build-linux: cross-compile for Linux (amd64)
build-linux:
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY)-linux-amd64 $(CMD)
	@echo "✓ Built $(BIN_DIR)/$(BINARY)-linux-amd64"

## build-windows: cross-compile for Windows (amd64)
build-windows:
	@mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY)-windows-amd64.exe $(CMD)
	@echo "✓ Built $(BIN_DIR)/$(BINARY)-windows-amd64.exe"

## build-all: cross-compile for all supported targets
build-all: build-linux build-windows

## run: build and run the agent (uses GEMINI_API_KEY by default)
run: build
	./$(BIN_DIR)/$(BINARY)

## test: run all unit tests
test:
	go test -v -race ./...

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go source files
fmt:
	gofmt -w .

## tidy: update go.sum and remove unused dependencies
tidy:
	go mod tidy

## clean: remove compiled binaries
clean:
	rm -rf $(BIN_DIR)
	@echo "✓ Cleaned"

## help: display this help message
help:
	@echo "Sagittarius A* (aig) — Makefile targets:"
	@grep -E '^## ' Makefile | sed 's/## /  /'
