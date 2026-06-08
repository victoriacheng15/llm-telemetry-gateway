# Python Runtime Makefile Targets

.PHONY: freeze install lint-py test-py cov-py fmt-py

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
