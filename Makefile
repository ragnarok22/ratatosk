.PHONY: dev-server dev-cli dev-dashboard build clean format lint test test-race coverage

dev-server:
	go run ./cmd/server

dev-cli:
	go run ./cmd/cli

dev-dashboard:
	cd cmd/server/dashboard && pnpm run dev

build:
	go build -o bin/server ./cmd/server
	go build -o bin/cli ./cmd/cli

format:
	gofmt -w .

lint:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

clean:
	rm -rf bin/ coverage.out
