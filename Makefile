# Every CI step is a target here, so `make ci` locally runs what GitHub Actions runs.
# Needs: Go (any recent; go.mod's go line fetches the exact toolchain), Node 26 with pnpm,
# Docker (Compose and buildx; CI passes DB_UP= E2E_DB_RESET= and needs none), Helm and kubeconform.

.DEFAULT_GOAL := help
SHELL := bash
.SHELLFLAGS := -euo pipefail -c

APP        ?= rom-request
IMAGE      ?= ghcr.io/oldsj/$(APP)
CHART      := charts/$(APP)
PLATFORMS  ?= linux/amd64,linux/arm64
GOTOOL     := go tool -modfile=tools/go.mod
PNPM       := pnpm --dir web

# Disposable Postgres from compose.yaml; tests make their own databases on it.
PG_PORT    ?= 54329
PG_ADMIN   := postgres://app:app-dev-only@localhost:$(PG_PORT)
export TEST_DATABASE_URL ?= $(PG_ADMIN)/app?sslmode=disable
E2E_DATABASE_URL ?= $(PG_ADMIN)/app_e2e?sslmode=disable
# CI passes DB_UP= and E2E_DB_RESET= because its jobs already have a fresh Postgres (a service
# container, no Docker) at TEST_DATABASE_URL / E2E_DATABASE_URL.
DB_UP ?= db-up
E2E_DB_RESET ?= e2e-db-reset

.PHONY: help
help: ## List targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

## ---- Development -------------------------------------------------------------------------

.PHONY: setup
setup: ## Install web dependencies and the Playwright browser
	$(PNPM) install --frozen-lockfile
	cd web && pnpm exec playwright install chromium

.env:
	cp .env.example .env

.PHONY: dev
dev: .env db-up ## Hot reload: Go API on :8080 (air), Vite on http://localhost:5173
	@set -a; source .env; set +a; \
	trap 'kill 0' INT TERM EXIT; \
	$(GOTOOL) air & \
	$(PNPM) dev & \
	wait

.PHONY: db-up
db-up: ## Start Postgres (Docker Compose)
	POSTGRES_PORT=$(PG_PORT) docker compose up -d --wait postgres

.PHONY: db-down
db-down: ## Stop Postgres and delete its data
	docker compose down -v

.PHONY: gen
gen: ## Regenerate Go server, sqlc queries and the TS client from api/ and internal/db/
	go generate ./...
	$(PNPM) gen

.PHONY: gen-check
gen-check: gen ## Fail if generated code or go.mod is stale
	go mod tidy
	cd tools && go mod tidy
	@git diff --exit-code -- . || { echo 'Generated code is stale: run make gen and commit'; exit 1; }
	@test -z "$$(git status --porcelain --untracked-files=all -- internal web/src go.mod go.sum tools)" || { git status --short; echo 'Untracked generated files'; exit 1; }

## ---- Checks ----------------------------------------------------------------------------

.PHONY: lint
lint: lint-go lint-web lint-api ## All linters

.PHONY: fmt
fmt: ## Format Go and fix what linters can
	$(GOTOOL) golangci-lint fmt ./...
	cd web && pnpm exec eslint --fix .

.PHONY: lint-go
lint-go:
	$(GOTOOL) golangci-lint run ./...

.PHONY: lint-web
lint-web:
	cd web && pnpm exec tsc -b && pnpm exec eslint .

.PHONY: lint-api
lint-api:
	cd web && pnpm exec redocly lint ../api/openapi.yaml --config ../redocly.yaml

.PHONY: design
design: ## Design system (web/design): build the previews, then check tokens, @dsCard markers, axe
	$(PNPM) build:design
	$(PNPM) check:design

.PHONY: test
test: test-go test-web ## Go (race, against real Postgres) and web unit tests

.PHONY: test-go
test-go: $(DB_UP)
	go test -race -count=1 ./...

.PHONY: test-web
test-web:
	cd web && pnpm exec vitest run

.PHONY: build
build: ## The single binary with the SPA embedded, in bin/app
	$(PNPM) build
	CGO_ENABLED=0 go build -tags embedweb -trimpath -o bin/app ./cmd/app

.PHONY: e2e
e2e: build $(E2E_DB_RESET) ## Playwright against bin/app and a fresh database
	cd web && DATABASE_URL='$(E2E_DATABASE_URL)' pnpm exec playwright test

.PHONY: e2e-db-reset
e2e-db-reset: db-up
	docker compose exec -T postgres psql -q -U app -d app \
		-c 'DROP DATABASE IF EXISTS app_e2e WITH (FORCE)' -c 'CREATE DATABASE app_e2e'

.PHONY: security
security: ## govulncheck, pnpm audit, and osv-scanner (every lockfile, tools included)
	$(GOTOOL) govulncheck ./...
	$(PNPM) audit --audit-level=moderate
	$(GOTOOL) osv-scanner scan source --recursive .

.PHONY: image
image: ## Build the multi-arch image (no push)
	@# Multi-platform builds need a docker-container builder; Docker Desktop's default can't.
	docker buildx inspect webapp-builder >/dev/null 2>&1 || \
		docker buildx create --name webapp-builder --driver docker-container >/dev/null
	docker buildx build --builder webapp-builder --platform $(PLATFORMS) -t $(IMAGE):dev .

.PHONY: chart
chart: ## helm lint, kubeconform -strict on every ci/ values file, chart version-bump check
	helm lint --strict $(CHART) -f $(CHART)/ci/default.yaml
	@for f in $(CHART)/ci/*.yaml; do \
		echo "kubeconform: $$f"; \
		helm template app $(CHART) -f $$f --namespace app | kubeconform -strict -summary \
			-kubernetes-version 1.34.0 \
			-schema-location default \
			-schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'; \
	done
	scripts/check-chart-version.sh $(CHART)

.PHONY: ci
ci: gen-check lint design test security chart e2e image ## Everything CI runs, in order
	@echo 'make ci: all checks passed'
