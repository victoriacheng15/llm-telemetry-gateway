# Kubernetes & Container Deployment Makefile Targets

.PHONY: lint-k3s build-image deploy scale-down scale-up test-k3s

lint-k3s: ## Lint Kubernetes manifests using kube-linter
	@echo "==> Linting Kubernetes manifests..."
	~/go/bin/kube-linter lint k3s/

build-image: ## Build the custom completions proxy Docker image and import it into k3s
	@echo "==> Building Docker image..."
	$(DOCKER) build -t llm-telemetry-gateway:v1.0.0 -f docker/gateway/Dockerfile .
	@echo "==> Importing Docker image into k3s..."
	$(DOCKER) save llm-telemetry-gateway:v1.0.0 | sudo k3s ctr images import -

deploy: ## Apply Kubernetes manifests to the cluster
	@echo "==> Applying bootstrap resources..."
	kubectl apply -f k3s/bootstrap/
	@echo "==> Applying telemetry stack..."
	kubectl apply -f k3s/telemetry/
	@echo "==> Applying Ollama environment..."
	kubectl apply -f k3s/ollama/
	@echo "==> Applying gateway RBAC configuration..."
	kubectl apply -f k3s/apps/rbac.yaml
	@echo "==> Applying gateway NetworkPolicy..."
	kubectl apply -f k3s/apps/network-policy.yaml
	@echo "==> Applying gateway workload deployment..."
	sed "s|/opt/llm-telemetry-gateway|$$PWD|g" k3s/apps/deployment.yaml | kubectl apply -f -

scale-down: ## Scale down all sandbox deployments to 0 replicas
	@echo "==> Scaling down all deployments to 0..."
	kubectl scale deployment --all -n gateway --replicas=0
	kubectl scale deployment --all -n telemetry --replicas=0
	kubectl scale deployment --all -n ollama --replicas=0
	kubectl scale deployment --all -n chaos-mesh --replicas=0

scale-up: ## Scale up all sandbox deployments to 1 replica
	@echo "==> Scaling up all deployments to 1..."
	kubectl scale deployment --all -n gateway --replicas=1
	kubectl scale deployment --all -n telemetry --replicas=1
	kubectl scale deployment --all -n ollama --replicas=1
	kubectl scale deployment --all -n chaos-mesh --replicas=1

test-k3s: ## Run cluster pod end-to-end loopback validation
	@echo "==> Verifying UDS socket mount inside pod..."
	kubectl exec -n gateway deploy/gateway -c gateway -- ls -la /tmp/shared
	@echo "==> Validating completions masking inside pod..."
	kubectl exec -n gateway deploy/gateway -c gateway -- wget -qO- \
		--post-data='{"prompt": "Client SSN is 123-45-6789"}' \
		--header='Content-Type: application/json' \
		http://localhost:8080/v1/chat/completions
