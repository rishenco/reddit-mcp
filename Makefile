BIN := bin/reddit-mcp
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build install test lint clean up

build:
	go build -trimpath -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/reddit-mcp

install:
	go install -trimpath -ldflags "-X main.version=$(VERSION)" ./cmd/reddit-mcp

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run

clean:
	rm -rf $(BIN)

up:
	set -a && . ./.env && set +a && docker compose up -d --build
