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

.PHONY: up
up: ## Start the local PostgreSQL via docker compose
	docker compose up -d postgres

.PHONY: down
down: ## Stop the local stack
	docker compose down

.PHONY: dev
dev: ## Run the API servers and the admin dashboard, auto-starting what is not up
	@if curl -sf -o /dev/null http://127.0.0.1:8080/admin/api/console-status; then \
		echo "  API       already running on :8080 (log: /tmp/openpay-api.log)"; \
	else \
		echo "  API       starting on :8080 (log: /tmp/openpay-api.log)..."; \
		nohup go run ./cmd/openpay serve > /tmp/openpay-api.log 2>&1 & \
	fi
	@if curl -sf -o /dev/null http://127.0.0.1:3000/login; then \
		echo "  Dashboard already running on :3000 (log: /tmp/openpay-dashboard.log)"; \
	else \
		echo "  Dashboard starting on :3000 (log: /tmp/openpay-dashboard.log)..."; \
		npm --prefix admin run dev > /tmp/openpay-dashboard.log 2>&1 & \
	fi
	@sleep 5
	@curl -sf -o /dev/null http://127.0.0.1:8080/admin/api/console-status \
		|| echo "  NOTE: the API is not answering yet — see the API log above."
	@curl -sf -o /dev/null http://127.0.0.1:3000/login \
		|| echo "  NOTE: the dashboard is not answering yet — see the dashboard log above."
	@echo "  Dashboard : http://localhost:3000"
	@echo "  API       : http://localhost:8080"

.PHONY: reset-onboarding
reset-onboarding: ## ⚠ Destructive: wipe ALL data and go back to the first-install setup
	@echo ""
	@echo "  WARNING: this permanently deletes the local openpay database,"
	@echo "  including:"
	@echo "    - console accounts, sessions and the audit log"
	@echo "    - merchants, API keys and merchant webhook secrets"
	@echo "    - stored gateway credentials (provider_configs, secrets included)"
	@echo "    - transactions and their webhooks, inbound callbacks, everything"
	@echo ""
	@echo "  Afterwards the database holds only migrations and the demo"
	@echo "  merchant, with NEW keys. The admin console's first-install"
	@echo "  wizard (/setup) becomes available again because no console"
	@echo "  account survives the reset."
	@echo ""
	@printf '  Type RESET to continue (anything else aborts): ' && read answer && { \
		[ "$$answer" = "RESET" ] || { echo '  Aborted — nothing was changed.'; exit 1; }; \
	}
	@docker compose down -v
	@$(MAKE) up
	@echo "  Waiting for PostgreSQL..."
	@for i in $$(seq 1 30); do \
		docker exec openpay-postgres pg_isready -U openpay >/dev/null 2>&1 && break; \
		[ $$i = 30 ] && { echo "  postgres did not become ready"; exit 1; }; \
		sleep 1; \
	done
	@$(MAKE) migrate
	@echo ""
	@echo "  Database wiped"
	@$(MAKE) dev
	@echo ""
	@echo "  The console reports it has no account yet:"
	@echo "  open http://localhost:3000 and walk the first-install"
	@echo "  setup (/setup) to create the owner account."
	@echo ""

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) coverage.out coverage.html
