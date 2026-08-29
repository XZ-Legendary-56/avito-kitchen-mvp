.PHONY: up down logs build test lint migrate-up migrate-down migrate-status seed generate

# Host-side defaults for talking to the Compose postgres from outside Docker
# (make migrate-up, make seed). 5434 because 5432/5433 are commonly already
# taken by native Postgres installs; see docker-compose.yml. Override any of
# these on the command line, e.g. `make migrate-up DB_PORT=5432`.
DB_HOST ?= localhost
DB_PORT ?= 5434
DB_USER ?= avito_kitchen
DB_PASSWORD ?= avito_kitchen
DB_NAME ?= avito_kitchen
DB_SSLMODE ?= disable
db_env = DB_HOST=$(DB_HOST) DB_PORT=$(DB_PORT) DB_USER=$(DB_USER) DB_PASSWORD=$(DB_PASSWORD) DB_NAME=$(DB_NAME) DB_SSLMODE=$(DB_SSLMODE)

up:
	docker compose up --build

down:
	docker compose down -v

logs:
	docker compose logs -f

build:
	go build ./...

test:
	go test ./...

migrate-up:
	$(db_env) go run ./cmd/migrate up

migrate-down:
	$(db_env) go run ./cmd/migrate down

migrate-status:
	$(db_env) go run ./cmd/migrate status

seed:
	$(db_env) go run ./cmd/seed

# Regenerates the HTTP server code from api/openapi/*.yaml (oapi-codegen,
# strict-server mode) and the usecase-layer test mocks (go.uber.org/mock).
# Run this every time you edit an .yaml spec or a ports.go file — both
# internal/generated and every mock_test.go are machine-written and get
# overwritten, never hand-edited.
generate:
	go tool oapi-codegen -config api/openapi/publicapi.cfg.yaml api/openapi/public.yaml
	go tool mockgen -source=internal/usecase/catalog/ports.go -destination=internal/usecase/catalog/mock_test.go -package=catalog_test
	go tool mockgen -source=internal/usecase/order/ports.go -destination=internal/usecase/order/mock_test.go -package=order_test
	go tool mockgen -source=internal/usecase/txmanager.go -destination=internal/usecase/order/mock_txmanager_test.go -package=order_test

# Interim lint target for this stage: gofmt + go vet. Stage 12 replaces this
# with golangci-lint once .golangci.yml (section 10.1 of PROMPT.md) is added.
lint:
	@fmtout="$$(gofmt -l .)"; \
	if [ -n "$$fmtout" ]; then \
		echo "gofmt needs to be run on:"; echo "$$fmtout"; exit 1; \
	fi
	go vet ./...
