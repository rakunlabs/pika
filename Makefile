BINARY    := pika
MAIN_FILE := cmd/$(BINARY)/main.go

BUILD_DATE := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
BUILD_COMMIT := $(shell git rev-parse --short HEAD)
VERSION := $(or $(IMAGE_TAG),$(shell git describe --tags --first-parent --match "v*" 2> /dev/null || echo v0.0.0))
PKG := $(shell go list -m | head -n 1)

.DEFAULT_GOAL := help

.PHONY: build
build: build-ui ## Build the Go binary
	@echo "> Building $(PROJECT) binary with goreleaser"
	goreleaser build --snapshot --clean --single-target

.PHONY: build-ui
build-ui: ## Build the frontend assets
	@cd _ui && pnpm run build
	@rm -rf internal/server/dist && mv _ui/dist internal/server/dist
	@echo > internal/server/dist/.gitkeep

.PHONY: run
run: ## Run the application
	go run $(MAIN_FILE)

.PHONY: lint
lint: ## Lint Go files
	@golangci-lint run ./...

.PHONY: test
test: ## Run unit tests
	@go test -v -race ./...

.PHONY: coverage
coverage: ## Run unit tests with coverage
	@go test -v -race -cover -coverpkg=./... -coverprofile=coverage.out -covermode=atomic ./...
	@go tool cover -func=coverage.out

.PHONY: help
help: ## Display this help screen
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
