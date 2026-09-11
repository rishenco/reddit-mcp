BIN := bin/reddit-mcp

.PHONY: build install up

build:
	go build -trimpath -o $(BIN) ./cmd/reddit-mcp

install:
	go install ./cmd/reddit-mcp

up:
	set -a && . ./.env && set +a && docker compose up -d --build
