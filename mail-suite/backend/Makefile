.PHONY: dev build test tidy clean

dev:
	go run ./cmd/server

build:
	GOOS=linux GOARCH=amd64 go build -o ./bin/mail-suite ./cmd/server

test:
	go test ./...

tidy:
	go mod tidy

clean:
	rm -rf ./bin
