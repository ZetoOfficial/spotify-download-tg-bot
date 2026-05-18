.PHONY: build test test-integration lint sqlc tidy run

build:
	go build -o bot ./cmd/bot

test:
	go test ./...

test-integration:
	go test -tags=integration ./...

lint:
	golangci-lint run

sqlc:
	sqlc generate

tidy:
	go mod tidy

run:
	go run ./cmd/bot
