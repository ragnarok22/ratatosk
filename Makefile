.PHONY: dev-server dev-cli dev-dashboard build build-dashboard clean format format-check lint test test-race coverage dashboard-format-check dashboard-lint dashboard-typecheck docs-dev docs-build docs-preview

dev-server: build-dashboard
	go run ./cmd/server

dev-cli:
	go run ./cmd/cli

dev-dashboard:
	cd cmd/server/dashboard && pnpm run dev

build-dashboard:
	cd cmd/server/dashboard && pnpm install && pnpm run build

build: build-dashboard
	go build -o bin/server ./cmd/server
	go build -o bin/cli ./cmd/cli

format:
	gofmt -w .

format-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Files not formatted with gofmt:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

lint:
	go vet -tags dev ./...

test:
	go test -tags dev ./...

test-race:
	go test -tags dev -race ./...

coverage:
	go test -tags dev -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

dashboard-format-check:
	cd cmd/server/dashboard && pnpm run format:check

dashboard-lint:
	cd cmd/server/dashboard && pnpm run lint

dashboard-typecheck:
	cd cmd/server/dashboard && pnpm run typecheck

clean:
	rm -rf bin/ coverage.out cmd/server/dashboard/dist

docs-dev:
	cd docs && pnpm run docs:dev

docs-build:
	cd docs && pnpm install && pnpm run docs:build

docs-preview:
	cd docs && pnpm run docs:preview
