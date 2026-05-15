.PHONY: up

up:
	set -a && . ./.env && set +a && docker compose up -d --build