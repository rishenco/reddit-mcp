BIN := bin/reddit-mcp
DIST := dist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build install test lint dist clean up

build:
	go build -trimpath -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/reddit-mcp

install:
	go install -trimpath -ldflags "-X main.version=$(VERSION)" ./cmd/reddit-mcp

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run

# Release archives for every supported platform, the same way CI builds them.
dist:
	VERSION=$(VERSION) DIST=$(DIST) ./scripts/build-dist.sh

clean:
	rm -rf $(BIN) $(DIST)

up:
	set -a && . ./.env && set +a && docker compose up -d --build
