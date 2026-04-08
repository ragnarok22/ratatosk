.PHONY: help dev-server dev-cli dev-dashboard build build-dashboard clean format format-check lint test test-race coverage dashboard-format-check dashboard-lint dashboard-typecheck docs-dev docs-build docs-preview

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-24s\033[0m %s\n", $$1, $$2}'

dev-server: build-dashboard ## Run the relay server with auto-rebuild
	go run ./cmd/server

dev-cli: ## Run the CLI client with auto-rebuild
	go run ./cmd/cli

dev-dashboard: ## Run the Vite dev server (React frontend)
	cd cmd/server/dashboard && pnpm run dev

build-dashboard: ## Build the React frontend
	cd cmd/server/dashboard && pnpm install && pnpm run build

build: build-dashboard ## Build both server and CLI binaries
	go build -o bin/server ./cmd/server
	go build -o bin/cli ./cmd/cli

format: ## Format Go source files
	gofmt -w .

format-check: ## Verify Go files are formatted
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Files not formatted with gofmt:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

lint: ## Run go vet
	go vet -tags dev ./...

test: ## Run all tests
	go test -tags dev ./...

test-race: ## Run tests with the race detector
	go test -tags dev -race ./...

coverage: ## Generate and display coverage report
	go test -tags dev -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

dashboard-format-check: ## Check dashboard code formatting
	cd cmd/server/dashboard && pnpm run format:check

dashboard-lint: ## Lint dashboard code
	cd cmd/server/dashboard && pnpm run lint

dashboard-typecheck: ## Type-check dashboard code
	cd cmd/server/dashboard && pnpm run typecheck

clean: ## Remove build artifacts
	rm -rf bin/ coverage.out cmd/server/dashboard/dist

docs-dev: ## Run VitePress dev server
	cd docs && pnpm run docs:dev

docs-build: ## Build documentation site
	cd docs && pnpm install && pnpm run docs:build

docs-preview: ## Preview built documentation
	cd docs && pnpm run docs:preview
