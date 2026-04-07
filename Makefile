.PHONY: dev-server dev-cli dev-dashboard build clean format lint

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

clean:
	rm -rf bin/
