# openpay.id - open source payment orchestration for Indonesia

BINARY      := openpay
MAIN        := ./cmd/openpay
BIN_DIR     := bin
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary into ./bin
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(MAIN)

.PHONY: run
run: ## Run the servers locally (loads .env)
	go run $(MAIN) serve

.PHONY: test
test: ## Run all tests
	go test ./...

.PHONY: test-race
test-race: ## Run tests with the race detector
	go test -race ./...

.PHONY: cover
cover: ## Run tests and write a coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format the code
	gofmt -w .

.PHONY: check
check: fmt vet test ## Format, vet and test

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy

.PHONY: migrate
migrate: ## Apply database migrations
	go run $(MAIN) migrate

.PHONY: seed
seed: ## Create a demo merchant and print its API key
	go run $(MAIN) seed --name "Demo Merchant" --callback-url "http://localhost:9090/hooks/openpay"

.PHONY: up
up: ## Start the local PostgreSQL via docker compose
	docker compose up -d postgres

.PHONY: down
down: ## Stop the local stack
	docker compose down

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) coverage.out coverage.html
