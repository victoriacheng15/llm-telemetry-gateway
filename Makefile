# Global Makefile configurations and flags
MAKEFLAGS += --no-print-directory

.PHONY: all freeze install lint lint-go lint-py lint-md test test-go test-py test-k3s fmt fmt-go fmt-py fmt-md cov cov-go cov-py build-go help

all: lint test fmt

# ==============================================================================
# PYTHON RUNTIME TARGETS (DEPENDENCY MANAGEMENT, LINTING, TESTING, FORMATTING)
# ==============================================================================

freeze: ## Freeze Python dependencies inside virtualenv to requirements.txt
	@echo "==> Freezing Python dependencies..."
	.venv/bin/pip freeze > requirements.txt

install: ## Install Python dependencies inside virtualenv from requirements.txt
	@echo "==> Installing Python dependencies..."
	python3 -m venv .venv
	.venv/bin/pip install -r requirements.txt

lint-py: ## Lint Python code
	@echo "==> Linting Python code..."
	@if [ -f .venv/bin/ruff ]; then \
		.venv/bin/ruff check cmd/ internal/; \
	else \
		echo "Warning: 'ruff' not found in virtualenv. Skipping Python linting."; \
	fi

test-py: ## Run Python tests using pytest in virtualenv
	@echo "==> Running Python unit tests..."
	PYTHONPATH=. .venv/bin/python -m pytest internal/sidecar/ -v

cov-py: ## Run Python test coverage using pytest-cov
	@echo "==> Running Python test coverage..."
	PYTHONPATH=. .venv/bin/python -m pytest --cov=internal/sidecar --cov-report=term-missing internal/sidecar/
	rm -f .coverage

fmt-py: ## Format Python code
	@echo "==> Formatting Python code..."
	@if [ -f .venv/bin/ruff ]; then \
		.venv/bin/ruff format cmd/ internal/; \
	else \
		echo "Warning: 'ruff' not found in virtualenv. Skipping Python formatting."; \
	fi

# ==============================================================================
# GO RUNTIME TARGETS (LINTING, TESTING, FORMATTING)
# ==============================================================================

lint-go: ## Lint Go code
	@echo "==> Linting Go code..."
	go vet ./...

test-go: ## Run Go tests
	@echo "==> Running Go unit tests..."
	go test -v ./...

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

# ==============================================================================
# MARKDOWN TARGETS (LINTING, FORMATTING)
# ==============================================================================

lint-md: ## Lint Markdown files
	@echo "==> Linting Markdown files..."
	npx markdownlint-cli '**/*.md' --ignore .venv

fmt-md: ## Format Markdown files using markdownlint-cli
	@echo "==> Formatting Markdown files..."
	npx markdownlint-cli '**/*.md' --ignore .venv --fix

# ==============================================================================
# KUBERNETES TARGETS (LINTING)
# ==============================================================================

lint-k3s: ## Lint Kubernetes manifests using kube-linter
	@echo "==> Linting Kubernetes manifests..."
	~/go/bin/kube-linter lint k3s/


# ==============================================================================
# COMPOSITE & AUTOMATION TARGETS
# ==============================================================================

lint: ## Run all linters
	@$(MAKE) lint-go
	@$(MAKE) lint-py
	@$(MAKE) lint-md
	@$(MAKE) lint-k3s

test: ## Run all tests
	@$(MAKE) test-go
	@$(MAKE) test-py

fmt: ## Format all code
	@$(MAKE) fmt-go
	@$(MAKE) fmt-py
	@$(MAKE) fmt-md

cov: ## Run all test coverages
	@$(MAKE) cov-go
	@$(MAKE) cov-py

test-k3s: ## Run cluster pod end-to-end loopback validation
	@echo "==> Verifying UDS socket mount inside pod..."
	kubectl exec -n gateway deploy/gateway -c gateway -- ls -la /tmp/shared
	@echo "==> Validating completions masking inside pod..."
	kubectl exec -n gateway deploy/gateway -c gateway -- wget -qO- \
		--post-data='{"prompt": "Client SSN is 123-45-6789"}' \
		--header='Content-Type: application/json' \
		http://localhost:8080/v1/chat/completions

# ==============================================================================
# DOCUMENTATION
# ==============================================================================

help: ## Show this help menu
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
