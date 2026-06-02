# Global Makefile configurations and flags
MAKEFLAGS += --no-print-directory

.PHONY: all freeze install lint lint-go lint-py test test-go test-py fmt fmt-go fmt-py help

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
	@if command -v ruff >/dev/null 2>&1; then \
		ruff check cmd/sidecar/*.py; \
	else \
		echo "Warning: 'ruff' not found in path. Skipping Python linting."; \
	fi

test-py: ## Run Python tests using pytest in virtualenv
	@echo "==> Running Python unit tests..."
	PYTHONPATH=cmd/sidecar .venv/bin/python -m pytest -v

fmt-py: ## Format Python code
	@echo "==> Formatting Python code..."
	@if command -v ruff >/dev/null 2>&1; then \
		ruff format cmd/sidecar/*.py; \
	else \
		echo "Warning: 'ruff' not found in path. Skipping Python formatting."; \
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

fmt-go: ## Format Go code
	@echo "==> Formatting Go code..."
	go fmt ./...

# ==============================================================================
# COMPOSITE & AUTOMATION TARGETS
# ==============================================================================

lint: ## Run all linters
	@$(MAKE) lint-go
	@$(MAKE) lint-py

test: ## Run all tests
	@$(MAKE) test-go
	@$(MAKE) test-py

fmt: ## Format all code
	@$(MAKE) fmt-go
	@$(MAKE) fmt-py

# ==============================================================================
# DOCUMENTATION
# ==============================================================================

help: ## Show this help menu
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
