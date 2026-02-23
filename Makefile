PROJECT    := at
MAIN_FILE := cmd/$(PROJECT)/main.go

LOCAL_BIN_DIR := $(PWD)/bin

BUILD_DATE := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
BUILD_COMMIT := $(shell git rev-parse --short HEAD || echo "unknown")
VERSION := $(or $(IMAGE_TAG),$(shell git describe --tags --first-parent --match "v*" 2> /dev/null || echo v0.0.0))

.DEFAULT_GOAL := help

.PHONY: run
run: ## Run the at command-line tool
	@go run $(MAIN_FILE)

.PHONY: run-ui
run-ui: ## Run the UI in development mode
	@cd _ui && pnpm run dev

.PHONY: env
env: ## Create environment
	@echo "> Creating environment $(PROJECT)"
	docker compose --project-name=$(PROJECT) --file=env/compose.yaml up -d

.PHONY: env-down
env-down: ## Destroy environment
	@echo "> Destroying environment $(PROJECT)"
	docker compose --project-name=$(PROJECT) down --volumes

.PHONY: install-ui
install-ui: ## Install UI dependencies
	@echo "> Installing UI dependencies"
	@cd _ui && pnpm install

.PHONY: build-ui
build-ui: install-ui ## Build the UI assets
	@echo "> Building UI assets"
	@cd _ui && pnpm run build
	@rm -rf internal/server/dist && mv _ui/dist internal/server/dist
	@echo > internal/server/dist/.gitkeep

.PHONY: build
build: build-ui ## Build the Go binary
	@echo "> Building $(PROJECT) binary with goreleaser"
	goreleaser build --snapshot --clean --single-target

.PHONY: build-container
build-container: build ## Build the container image with test tag
	docker build -t $(PROJECT):test -f ci/Dockerfile dist/at_linux_amd64_v1/

.PHONY: lint
lint: ## Lint Go files
	@GOPATH="$(shell dirname $(PWD))" golangci-lint run ./...

.PHONY: test
test: ## Run unit tests
	@go test -v -race ./...

.PHONY: help
help: ## Display this help screen
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
