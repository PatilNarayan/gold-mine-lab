.PHONY: run build test test-integration vet lint db-up db-down

run:
	go run ./cmd/server

build:
	go build -o bin/server.exe ./cmd/server

test:
	go test ./...

test-integration:
	go test -tags=integration ./tests/

vet:
	go vet ./...

lint:
	go vet ./... && gofmt -l .

db-up:
	docker compose up -d postgres

db-down:
	docker compose down
