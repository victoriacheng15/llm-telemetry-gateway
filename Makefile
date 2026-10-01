# Global Makefile configurations and flags
MAKEFLAGS += --no-print-directory

.PHONY: all
all: lint test fmt

# ==============================================================================
# MARKDOWN TARGETS
# ==============================================================================

.PHONY: lint-md fmt-md

lint-md: ## Lint Markdown files
	@echo "==> Linting Markdown files..."
	npx markdownlint-cli '**/*.md' --ignore .venv

fmt-md: ## Format Markdown files using markdownlint-cli
	@echo "==> Formatting Markdown files..."
	npx markdownlint-cli '**/*.md' --ignore .venv --fix

# ==============================================================================
# GO TARGETS
# ==============================================================================

.PHONY: update lint-go test-go test-bdd cov-go fmt-go build-go build-showcase

update: ## Update Go dependencies
	@echo "==> Updating Go dependencies..."
	go get -u ./...
	go mod tidy

lint-go: ## Lint Go code
	@echo "==> Linting Go code..."
	go vet ./...

test-go: ## Run Go unit tests
	@echo "==> Running Go unit tests..."
	go test -v $(shell go list ./... | grep -v /e2e)

test-bdd: ## Run Go BDD tests
	@echo "==> Running Go BDD E2E tests..."
	go test -v ./e2e/...

cov-go: ## Run Go test coverage
	@echo "==> Running Go test coverage..."
	go test -cover -coverprofile=coverage.out ./...
	rm -f coverage.out

fmt-go: ## Format Go code
	@echo "==> Formatting Go code..."
	go fmt ./...

build-go: ## Build the Go gateway binary statically
	@echo "==> Building Go gateway binary..."
	CGO_ENABLED=0 go build -ldflags "-extldflags -static" -o bin/gateway cmd/gateway/main.go

build-showcase: ## Build the showcase static site
	@echo "==> Preparing dist directory..."
	rm -rf dist
	mkdir -p dist
	@echo "==> Building showcase static site..."
	go run cmd/showcase/main.go


# ==============================================================================
# PYTHON TARGETS
# ==============================================================================

.PHONY: install lock lint-py test-py cov-py fmt-py

install: ## Install Python dependencies using uv sync
	@echo "==> Installing Python dependencies with uv..."
	uv sync

lock: ## Generate or update uv.lock lockfile
	@echo "==> Locking Python dependencies with uv..."
	uv lock

lint-py: ## Lint Python code using ruff
	@echo "==> Linting Python code..."
	uv run ruff check cmd/ internal/

test-py: ## Run Python unit tests using pytest
	@echo "==> Running Python unit tests..."
	uv run pytest internal/sidecar/ -v

cov-py: ## Run Python test coverage using pytest-cov
	@echo "==> Running Python test coverage..."
	uv run pytest --cov=internal/sidecar --cov-report=term-missing internal/sidecar/
	rm -f .coverage

fmt-py: ## Format Python code using ruff
	@echo "==> Formatting Python code..."
	uv run ruff format cmd/ internal/

# ==============================================================================
# KUBERNETES & CONTAINER TARGETS
# ==============================================================================

.PHONY: lint-k3s bootstrap teardown port-forward port-forward-bg port-forward-stop port-forward-status test-k3s

bootstrap: ## Bootstrap local cluster, compile binary, apply manifests, and warm models
	@echo "==> Bootstrapping local Kubernetes environment..."
	bash scripts/bootstrap.sh

teardown: ## Teardown local cluster resources, port-forwards, and namespaces
	@echo "==> Tearing down local Kubernetes environment..."
	bash scripts/teardown.sh

port-forward: ## Run local port-forwards in the foreground
	@echo "==> Launching local port-forwarding session..."
	bash scripts/port-forward.sh run all

port-forward-bg: ## Start local port-forwards in the background
	@echo "==> Starting background port-forwarding..."
	bash scripts/port-forward.sh start all

port-forward-stop: ## Stop background port-forwards
	@echo "==> Stopping background port-forwarding..."
	bash scripts/port-forward.sh stop

port-forward-status: ## Check status of port-forwarded endpoints
	bash scripts/port-forward.sh status

lint-k3s: ## Lint Kubernetes manifests using kube-linter
	@echo "==> Linting Kubernetes manifests..."
	~/go/bin/kube-linter lint k3s/

test-k3s: ## Run cluster pod end-to-end loopback validation
	@echo "==> Verifying UDS socket mount inside pod..."
	kubectl exec -n gateway deploy/gateway -c gateway -- ls -la /tmp/shared
	@echo "==> Validating completions masking inside pod..."
	kubectl exec -n gateway deploy/gateway -c gateway -- wget -qO- \
		--post-data='{"model": "qwen2.5:0.5b", "messages": [{"role": "user", "content": "Client SSN is 123-45-6789"}]}' \
		--header='Content-Type: application/json' \
		http://localhost:8080/v1/chat/completions

# ==============================================================================
# SHOWCASE DEV CONTAINER TARGETS
# ==============================================================================

CONTAINER_ENGINE  ?= podman
SHOWCASE_IMAGE    ?= showcase-dev
SHOWCASE_CONTAINER ?= showcase-dev

.PHONY: showcase-build showcase-run showcase-logs showcase-clean

showcase-build: ## Build the showcase development container image
	@echo "==> Building showcase development container image..."
	$(CONTAINER_ENGINE) build -t $(SHOWCASE_IMAGE) -f docker/showcase/Dockerfile .

showcase-run: ## Run the showcase container in dev mode with live reload
	@echo "==> Running showcase dev container on http://localhost:3000..."
	$(CONTAINER_ENGINE) run --rm -it \
		-v "$(PWD)/cmd":/workspace/cmd:Z \
		-v "$(PWD)/internal/web/showcase":/workspace/internal/web/showcase:Z \
		-p 3000:3000 \
		--name $(SHOWCASE_CONTAINER) \
		$(SHOWCASE_IMAGE)

showcase-logs: ## Follow logs of the running showcase dev container
	$(CONTAINER_ENGINE) logs -f $(SHOWCASE_CONTAINER)

showcase-clean: ## Stop and remove the showcase dev container and image
	@echo "==> Cleaning up showcase dev container..."
	-$(CONTAINER_ENGINE) stop $(SHOWCASE_CONTAINER)
	$(CONTAINER_ENGINE) rmi --force $(SHOWCASE_IMAGE)
	@echo "Image '$(SHOWCASE_IMAGE)' removed."

# ==============================================================================
# COMPOSITE & AUTOMATION TARGETS
# ==============================================================================

.PHONY: lint test fmt cov

lint: ## Run all linters
	@$(MAKE) lint-go
	@$(MAKE) lint-py
	@$(MAKE) lint-md
	@$(MAKE) lint-k3s

test: ## Run all tests
	@$(MAKE) test-go
	@$(MAKE) test-bdd
	@$(MAKE) test-py

fmt: ## Format all code
	@$(MAKE) fmt-go
	@$(MAKE) fmt-py
	@$(MAKE) fmt-md

cov: ## Run all test coverages
	@$(MAKE) cov-go
	@$(MAKE) cov-py

# ==============================================================================
# DOCUMENTATION
# ==============================================================================

.PHONY: help

help: ## Show this help menu
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'
