.PHONY: up down logs build test lint

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

# Interim lint target for this stage: gofmt + go vet. Stage 12 replaces this
# with golangci-lint once .golangci.yml (section 10.1 of PROMPT.md) is added.
lint:
	@fmtout="$$(gofmt -l .)"; \
	if [ -n "$$fmtout" ]; then \
		echo "gofmt needs to be run on:"; echo "$$fmtout"; exit 1; \
	fi
	go vet ./...
