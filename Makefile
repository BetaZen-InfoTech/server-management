# =============================================================================
# ServerPanel — Makefile
# =============================================================================

.PHONY: help dev dev-backend dev-frontend build build-backend build-frontend \
        docker-up docker-down docker-build lint test clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------------------
# Development
# ---------------------------------------------------------------------------

dev: ## Start both backend and frontend in dev mode
	@echo "Starting backend and frontend..."
	@make -j2 dev-backend dev-frontend

dev-backend: ## Start Go backend with Air hot-reload
	cd backend && air

dev-frontend: ## Start frontend dev servers (Turborepo)
	cd frontend && npm run dev

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

build: build-backend build-frontend ## Build everything for production

build-backend: ## Build Go binary
	cd backend && CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/server ./cmd/server
	cd backend && CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/agent ./cmd/agent

build-frontend: ## Build frontend SPAs
	cd frontend && npm ci && npx turbo run build

# ---------------------------------------------------------------------------
# Docker
# ---------------------------------------------------------------------------

docker-up: ## Start all services with Docker Compose
	docker compose up -d

docker-down: ## Stop all Docker services
	docker compose down

docker-build: ## Build Docker images
	docker compose build

# ---------------------------------------------------------------------------
# Quality
# ---------------------------------------------------------------------------

lint: ## Run linters
	cd backend && golangci-lint run ./...
	cd frontend && npx turbo run lint

test: ## Run all tests
	cd backend && go test ./...
	cd frontend && npx turbo run test

# ---------------------------------------------------------------------------
# Utilities
# ---------------------------------------------------------------------------

clean: ## Remove build artifacts
	rm -rf backend/bin backend/tmp
	rm -rf frontend/apps/whm/dist frontend/apps/cpanel/dist
	rm -rf frontend/node_modules frontend/apps/*/node_modules frontend/packages/*/node_modules

setup: ## Install all dependencies
	cd backend && go mod download
	cd frontend && npm install
