SHELL := /bin/sh

PNPM ?= pnpm
GO ?= go
GOPROXY ?= $(shell $(GO) env GOPROXY)
COMPOSE ?= docker compose
COMPOSE_FILE ?= deploy/compose/docker-compose.yml

# Local baseline verified on 2026-08-10:
# Go 1.27.1, Node 24.18.0, pnpm 10.33.0, Docker Compose v5.2.0.

.PHONY: lint test build generate contract-check frontend-checks migration-config-check image-build compose-up test-concurrency test-e2e test-security backup-db backup-images restore-drill preflight smoke rollback

lint:
	$(MAKE) -C backend lint
	$(PNPM) -r --if-present lint

test:
	$(MAKE) -C backend test
	$(PNPM) -r --if-present test

build:
	$(MAKE) -C backend build
	$(PNPM) -r --if-present build

generate:
	$(PNPM) generate

contract-check:
	$(PNPM) contract-check

frontend-checks:
	$(PNPM) --filter miniapp test
	$(PNPM) --filter admin test
	$(PNPM) --filter miniapp build:weapp
	$(PNPM) --filter admin build

migration-config-check:
	@set -eu; \
	$(COMPOSE) -f "$(COMPOSE_FILE)" config --format json | node tools/deploy/validate-compose.mjs; \
	for migration in backend/migrations/0001_init.up.sql backend/migrations/0002_seed.up.sql; do \
		test -s "$$migration"; \
	done

image-build:
	docker build --build-arg "GOPROXY=$(GOPROXY)" --file deploy/compose/Dockerfile --tag "$${APP_IMAGE:-shop-mall-app:local}" .

compose-up:
	@if [ ! -f "$(COMPOSE_FILE)" ]; then \
		printf '%s\n' "compose-up unavailable: $(COMPOSE_FILE) is missing." >&2; \
		exit 1; \
	fi
	$(COMPOSE) -f "$(COMPOSE_FILE)" up -d

# T15 acceptance gates. Each target starts/tears down its own isolated
# PostgreSQL container (or reuses TEST_DATABASE_URL when set) and drives the
# composed backend over real TLS HTTP. The backend is composed in-process as
# an httptest server (backend/tests/e2e/harness.go) with the fake WeChat
# client (T6) wired to the contract's wx-login route; the miniapp/admin test
# servers are their TypeScript suites under `make test`.

# Order/points concurrency: the backend integration suite plus the HTTP-level
# idempotency re-drive (same-token replay, token conflict 409, failed retry).
test-concurrency:
	cd backend && $(GO) test ./tests/integration/ -count=1 -timeout 20m
	cd backend && $(GO) test ./tests/e2e/ -run 'TestBuyerOrderIdempotency|TestBuyerOrderFailedRetry' -count=1 -timeout 20m

# Full end-to-end acceptance: buyer flows, admin flows, and the security suite.
test-e2e:
	cd backend && $(GO) test ./tests/e2e/... ./tests/security/... -count=1 -timeout 20m

# Security acceptance only (IDOR, admin 403/422, CSRF/Origin, stale token,
# upload bypass attempts).
test-security:
	cd backend && $(GO) test ./tests/security/... -count=1 -timeout 20m

# --- T16 backup / restore / release rehearsal --------------------------------
#
# backup-db / backup-images target the running compose stack. They default to
# BACKUP_ROOT=/var/backups/shop-mall and the compose file under deploy/compose.
# restore-drill runs the isolated end-to-end restore drill and reports measured
# RPO/RTO; it is the T16 acceptance gate. preflight/smoke are the release gates.

backup-db:
	bash deploy/backup/backup-db.sh

backup-images:
	bash deploy/backup/backup-images.sh

restore-drill:
	bash deploy/backup/drill.sh

preflight:
	bash deploy/release/preflight.sh

smoke:
	bash deploy/release/smoke.sh

rollback:
	bash deploy/release/rollback.sh

# --- admin E2E loop (Playwright, slice 1) -----------------------------------
#
# Boots a disposable PostgreSQL container on 127.0.0.1:15432, builds the two
# backend binaries into admin/e2e/.build, then runs the Playwright suite. The
# suite itself starts (and reuses across runs) the backend on :18080 and the
# https dev server on :8000 with the /api same-origin proxy. First run needs
# `pnpm --filter admin exec playwright install chromium`. The PG data dir is a
# tmpfs mount: the postgres image's VOLUME declaration would otherwise leave
# one anonymous volume behind on every run (docker rm without -v keeps it).

ADMIN_E2E_PG_PORT ?= 15432
ADMIN_E2E_PG_CONTAINER ?= shop-mall-admin-e2e-pg

.PHONY: admin-e2e admin-e2e-down

admin-e2e:
	@set -eu; \
	bash admin/e2e/certs/generate-certs.sh; \
	docker rm -f $(ADMIN_E2E_PG_CONTAINER) >/dev/null 2>&1 || true; \
	MSYS_NO_PATHCONV=1 docker run -d --name $(ADMIN_E2E_PG_CONTAINER) \
	  --tmpfs /var/lib/postgresql/data \
	  -e POSTGRES_USER=test -e POSTGRES_PASSWORD=test -e POSTGRES_DB=shop_mall_test \
	  -p 127.0.0.1:$(ADMIN_E2E_PG_PORT):5432 postgres:16-alpine >/dev/null; \
	until docker exec $(ADMIN_E2E_PG_CONTAINER) pg_isready -U test -d shop_mall_test >/dev/null 2>&1; do sleep 1; done; \
	cd backend && $(GO) build -o ../admin/e2e/.build/server.exe ./cmd/server && $(GO) build -o ../admin/e2e/.build/bootstrap.exe ./cmd/admin-bootstrap; \
	$(PNPM) --filter admin test:e2e

admin-e2e-down:
	@docker rm -f $(ADMIN_E2E_PG_CONTAINER) >/dev/null 2>&1 || true
