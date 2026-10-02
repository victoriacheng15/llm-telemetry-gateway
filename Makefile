# Global Makefile configurations and flags
MAKEFLAGS += --no-print-directory

# Scope variables with sensible defaults
GO_PKG         ?= ./...
PY_TARGET      ?= cmd/ internal/ scripts/
PY_TEST_TARGET ?= internal/sidecar/
MD_TARGET      ?= '**/*.md'

# Support positional file/directory arguments (e.g. make fmt internal/sidecar/*.py)
CMD  := $(firstword $(MAKECMDGOALS))
ARGS := $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))
ifneq ($(ARGS),)
  $(eval .PHONY: $(ARGS))
  $(eval $(ARGS):;@:)
endif

GO_FILES := $(filter %.go,$(ARGS))
PY_FILES := $(filter %.py,$(ARGS))
MD_FILES := $(filter %.md,$(ARGS))
DIR_ARGS := $(filter-out $(GO_FILES) $(PY_FILES) $(MD_FILES),$(ARGS))

.PHONY: all
all: lint test fmt


# ==============================================================================
# GO TARGETS
# ==============================================================================

.PHONY: update test-go test-bdd cov-go build-go

update: ## Update Go dependencies
	@echo "==> Updating Go dependencies..."
	go get -u ./...
	go mod tidy

test-go: ## Run Go unit tests (override: GO_PKG=...)
	@echo "==> Running Go unit tests..."
	go test -v $(if $(filter ./...,$(GO_PKG)),$(shell go list ./... | grep -v /e2e),$(GO_PKG))

test-bdd: ## Run Go BDD tests
	@echo "==> Running Go BDD E2E tests..."
	go test -v ./e2e/...

cov-go: ## Run Go test coverage
	@echo "==> Running Go test coverage..."
	go test -cover -coverprofile=coverage.out ./...
	rm -f coverage.out

build-go: ## Build the Go gateway binary statically
	@echo "==> Building Go gateway binary..."
	CGO_ENABLED=0 go build -ldflags "-extldflags -static" -o bin/gateway cmd/gateway/main.go


# ==============================================================================
# PYTHON TARGETS
# ==============================================================================

.PHONY: install lock test-py cov-py

install: ## Install Python dependencies using uv sync
	@echo "==> Installing Python dependencies with uv..."
	uv sync

lock: ## Generate or update uv.lock lockfile
	@echo "==> Locking Python dependencies with uv..."
	uv lock

test-py: ## Run Python unit tests using pytest (e.g., make test-py internal/sidecar/test_policy.py)
	@echo "==> Running Python unit tests..."
	uv run pytest $(if $(ARGS),$(ARGS),$(PY_TEST_TARGET)) -v

cov-py: ## Run Python test coverage using pytest-cov (override: PY_TEST_TARGET=...)
	@echo "==> Running Python test coverage..."
	uv run pytest --cov=internal/sidecar --cov-report=term-missing $(if $(ARGS),$(ARGS),$(PY_TEST_TARGET))
	rm -f .coverage

# ==============================================================================
# KUBERNETES & CONTAINER TARGETS
# ==============================================================================

.PHONY: lint-k3s bootstrap bootstrap-chaos teardown teardown-chaos port-forward port-forward-bg port-forward-stop port-forward-status test-k3s

bootstrap: ## Bootstrap local cluster, compile binary, apply manifests, and warm models
	@echo "==> Bootstrapping local Kubernetes environment..."
	bash scripts/bootstrap.sh $(ARGS)

bootstrap-chaos: ## Bootstrap local cluster with Chaos Mesh installed
	@echo "==> Bootstrapping local Kubernetes environment with Chaos Mesh..."
	bash scripts/bootstrap.sh --with-chaos

teardown: ## Teardown local cluster resources, port-forwards, and namespaces
	@echo "==> Tearing down local Kubernetes environment..."
	bash scripts/teardown.sh $(ARGS)

teardown-chaos: ## Teardown local cluster resources including Chaos Mesh
	@echo "==> Tearing down local Kubernetes environment and Chaos Mesh..."
	bash scripts/teardown.sh --with-chaos

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

lint: ## Run all linters (or e.g. make lint internal/sidecar/*.py)
ifeq ($(ARGS),)
	@echo "==> Linting Go code..."
	go vet $(GO_PKG)
	@echo "==> Linting Python code..."
	uv run ruff check $(PY_TARGET)
	@echo "==> Linting Markdown files..."
	npx markdownlint-cli $(MD_TARGET) --ignore .venv
else
	@if [ -n "$(GO_FILES)" ]; then echo "==> Linting Go code..."; go vet $(GO_FILES); fi
	@if [ -n "$(PY_FILES)" ]; then echo "==> Linting Python code..."; uv run ruff check $(PY_FILES); fi
	@if [ -n "$(MD_FILES)" ]; then echo "==> Linting Markdown files..."; npx markdownlint-cli $(MD_FILES) --ignore .venv; fi
	@if [ -n "$(DIR_ARGS)" ]; then \
		echo "==> Linting directories: $(DIR_ARGS)..."; \
		go vet $$(find $(DIR_ARGS) -name '*.go' 2>/dev/null) 2>/dev/null || true; \
		uv run ruff check $(DIR_ARGS) 2>/dev/null || true; \
	fi
endif

test: test-go test-bdd test-py ## Run all tests

fmt: ## Format all code (or e.g. make fmt internal/sidecar/*.py)
ifeq ($(ARGS),)
	@echo "==> Formatting Go code..."
	go fmt $(GO_PKG)
	@echo "==> Formatting Python code..."
	uv run ruff format $(PY_TARGET)
	@echo "==> Formatting Markdown files..."
	npx markdownlint-cli $(MD_TARGET) --ignore .venv --fix
else
	@if [ -n "$(GO_FILES)" ]; then echo "==> Formatting Go code..."; go fmt $(GO_FILES); fi
	@if [ -n "$(PY_FILES)" ]; then echo "==> Formatting Python code..."; uv run ruff format $(PY_FILES); fi
	@if [ -n "$(MD_FILES)" ]; then echo "==> Formatting Markdown files..."; npx markdownlint-cli $(MD_FILES) --ignore .venv --fix; fi
	@if [ -n "$(DIR_ARGS)" ]; then \
		echo "==> Formatting directories: $(DIR_ARGS)..."; \
		go fmt $(DIR_ARGS); \
		uv run ruff format $(DIR_ARGS); \
	fi
endif

cov: cov-go cov-py ## Run all test coverages

# ==============================================================================
# DOCUMENTATION
# ==============================================================================

.PHONY: help

help: ## Show this help menu
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'
