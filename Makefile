APP=server

.PHONY: test integration-test compose-up compose-down seed-dev migrate-up migrate-down

test:
	go test ./...

integration-test:
	go test ./tests/integration/...

migrate-up:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

compose-up:
	docker compose up -d --build

compose-down:
	docker compose down

seed-dev:
	sh scripts/seed-dev.sh
