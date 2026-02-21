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

.PHONY: env
env: ## Create environment
	@echo "> Creating environment $(PROJECT)"
	docker compose --project-name=$(PROJECT) --file=env/compose.yaml up -d

.PHONY: env-down
env-down: ## Destroy environment
	@echo "> Destroying environment $(PROJECT)"
	docker compose --project-name=$(PROJECT) down --volumes

.PHONY: build-ui
build-ui: ## Build the UI assets
	@echo "> Building UI assets"
	@cd _ui && pnpm install && pnpm run build
	@rm -rf internal/server/dist && mv _ui/dist internal/server/dist
	@echo > internal/server/dist/.gitkeep

.PHONY: lint
lint: ## Lint Go files
	@GOPATH="$(shell dirname $(PWD))" golangci-lint run ./...

.PHONY: test
test: ## Run unit tests
	@go test -v -race ./...

.PHONY: help
help: ## Display this help screen
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
