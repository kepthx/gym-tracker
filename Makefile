VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "%-16s %s\n", $$1, $$2}'

.PHONY: web
web: ## Build the frontend into the directory embedded in the binary
	npm --prefix web ci --silent || npm --prefix web install --silent
	npm --prefix web run build

.PHONY: build
build: web ## Build the binary for the current platform
	go build -trimpath -ldflags "$(LDFLAGS)" -o gymtracker ./cmd/gymtracker

.PHONY: release
release: web ## Build the binary for the server (Linux amd64)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
		go build -trimpath -ldflags "$(LDFLAGS)" -o dist/gymtracker-linux-amd64 ./cmd/gymtracker
	@ls -lh dist/gymtracker-linux-amd64

.PHONY: test
test: ## Run every test
	go test -race ./...
	npm --prefix web run test

.PHONY: e2e
e2e: build ## End-to-end browser scenario: offline, restart, catch-up sync
	npm --prefix web run e2e

.PHONY: check
check: ## Formatting, static analysis, types
	gofmt -l .
	go vet ./...
	npm --prefix web run typecheck

.PHONY: dev
dev: ## Start the development server (frontend separately: npm --prefix web run dev)
	GYM_ADDR=127.0.0.1:8071 go run ./cmd/gymtracker

.PHONY: clean
clean:
	rm -rf gymtracker dist internal/web/dist/assets internal/web/dist/index.html
