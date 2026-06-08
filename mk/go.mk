# Go Runtime Makefile Targets

.PHONY: lint-go test-go cov-go fmt-go build-go

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
