# Global Makefile configurations and flags
MAKEFLAGS += --no-print-directory

.PHONY: all lint lint-go lint-py test test-go test-py fmt fmt-go fmt-py help

all: lint test fmt

# ==============================================================================
# LINTING TARGETS (VERIFICATION FIRST)
# ==============================================================================

lint: ## Run all linters
	@$(MAKE) lint-go
	@$(MAKE) lint-py

lint-go: ## Lint Go code
	@echo "==> Linting Go code..."
	go vet ./...

lint-py: ## Lint Python code
	@echo "==> Linting Python code..."
	@if command -v ruff >/dev/null 2>&1; then \
		ruff check cmd/sidecar/*.py; \
	else \
		echo "Warning: 'ruff' not found in path. Skipping Python linting."; \
	fi

# ==============================================================================
# TESTING TARGETS (BEHAVIORAL CORRECTNESS)
# ==============================================================================

test: ## Run all tests
	@$(MAKE) test-go
	@$(MAKE) test-py

test-go: ## Run Go tests
	@echo "==> Running Go unit tests..."
	go test -v ./...

test-py: ## Run Python tests
	@echo "==> Running Python unit tests..."
	@if command -v pytest >/dev/null 2>&1; then \
		pytest; \
	elif python3 -m unittest discover >/dev/null 2>&1; then \
		python3 -m unittest discover; \
	else \
		echo "Warning: Neither 'pytest' nor 'unittest' found in path. Skipping Python tests."; \
	fi

# ==============================================================================
# FORMATTING TARGETS (STYLE COMPLIANCE)
# ==============================================================================

fmt: ## Format all code
	@$(MAKE) fmt-go
	@$(MAKE) fmt-py

fmt-go: ## Format Go code
	@echo "==> Formatting Go code..."
	go fmt ./...

fmt-py: ## Format Python code
	@echo "==> Formatting Python code..."
	@if command -v ruff >/dev/null 2>&1; then \
		ruff format cmd/sidecar/*.py; \
	else \
		echo "Warning: 'ruff' not found in path. Skipping Python formatting."; \
	fi

# ==============================================================================
# DOCUMENTATION
# ==============================================================================

help: ## Show this help menu
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
