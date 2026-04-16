# FlareX — Makefile

BIN_DIR      := bin
BIN          := $(BIN_DIR)/flarex
MOCK         := $(BIN_DIR)/mockworker
VERSION      := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT       := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE   := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS      := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)
GO           := go
GOFLAGS      := -trimpath

SOURCES := $(shell find . -type f -name '*.go' -not -path './bin/*' 2>/dev/null)

WEB_DIR  := web
WEB_OUT  := internal/admin/webui/dist
# NPM or PNPM — whichever is installed. npm is ubiquitous; pnpm faster if present.
NPM      := $(shell command -v pnpm 2>/dev/null || command -v npm)

.PHONY: all build build-mock rebuild test test-race bench cover lint vet fmt run clean docker docker-ui docker-bench help \
        web-deps web-build web-dev web-clean

all: build ## Build flarex + mockworker

help: ## Show this help
	@awk 'BEGIN {FS=":.*##"; printf "\n\033[1mAvailable targets:\033[0m\n"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: $(BIN) $(MOCK) ## Build binaries (incremental)

rebuild: ## Force rebuild both binaries
	@rm -f $(BIN) $(MOCK)
	@$(MAKE) --no-print-directory build

$(BIN): $(SOURCES) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $@ ./cmd/flarex

$(MOCK): $(SOURCES) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $@ ./cmd/mockworker

test: ## Run unit tests
	$(GO) test -timeout 60s ./...

test-race: ## Run tests with race detector
	$(GO) test -race -timeout 120s ./...

bench: ## Run benchmarks
	$(GO) test -bench=. -benchmem -benchtime=3s -run=^$$ ./internal/proxy/

web-deps: ## Install frontend deps (one-off; reruns are cheap)
	@test -n "$(NPM)" || (echo "need npm or pnpm in PATH"; exit 1)
	# `npm ci` is reproducible (no lockfile drift) and fail-loud when
	# package-lock is out of sync with package.json. Required in CI to
	# keep `git status` clean for goreleaser.
	cd $(WEB_DIR) && $(NPM) ci

web-build: web-deps ## Build production SPA → internal/admin/webui/dist (embedded on next go build)
	cd $(WEB_DIR) && $(NPM) run build
	# Vite empties outDir at every build, wiping the committed .gitkeep.
	# Re-create it so git stays clean (otherwise goreleaser refuses).
	@touch $(WEB_OUT)/.gitkeep

web-dev: web-deps ## Run Vite dev server on :5173 (proxies API calls → :9090)
	cd $(WEB_DIR) && $(NPM) run dev

web-clean: ## Wipe node_modules + built bundle
	rm -rf $(WEB_DIR)/node_modules $(WEB_OUT)
	@mkdir -p $(WEB_OUT) && echo placeholder > $(WEB_OUT)/.gitkeep

cover: ## HTML coverage report
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "coverage.html generated"

lint: ## Lint (go vet)
	$(GO) vet ./...

vet: lint

fmt: ## Format code
	$(GO) fmt ./...
	gofmt -s -w .

run: $(BIN) ## Run serve with config.yaml
	$(BIN) -c config.yaml serve

deploy: $(BIN) ## Deploy Workers to Cloudflare
	$(BIN) -c config.yaml deploy

destroy: $(BIN) ## Delete CF Workers
	$(BIN) -c config.yaml destroy

mock: $(MOCK) ## Run mockworker on :8787
	MOCK_HMAC_SECRET=$${MOCK_HMAC_SECRET:-testsecret} $(MOCK) --addr 127.0.0.1:8787

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) coverage.out coverage.html *.db

docker: ## Build Docker image (multi-stage: node + go, SPA always embedded)
	docker build -t flarex:$(VERSION) -t flarex:latest .

docker-ui: docker ## Alias for docker (kept for discoverability)
	@echo "→ flarex:$(VERSION) built (enable the SPA at runtime with admin.ui: true)"

docker-bench: ## Run the E2E test compose stack (mockworker + bench)
	docker-compose -f test/deploy/docker-compose.yml up --build --exit-code-from bench
