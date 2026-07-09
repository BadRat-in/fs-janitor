# FS Janitor Makefile
#
# Thin wrapper around the Go toolchain for building, testing, and releasing the
# `fsj` binary. FS Janitor is a pure-Go, macOS-only tool (no cgo — the SQLite
# driver is modernc.org/sqlite), so there are no special toolchain flags; the
# targets below standardise the common commands and stamp the version in.
#
# Run `make` or `make help` to list targets.

BINARY  := fsj
PKG     := ./cmd/fsj
BIN_DIR := bin

# Version stamped into the binary via -ldflags. Defaults to the current git
# describe (tag/sha), overridable: `make build VERSION=1.2.3`.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

FMT_DIRS := internal cmd

.DEFAULT_GOAL := help
.PHONY: help build install run test vet fmt fmt-check lint check clean build-all release

help: ## List available targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	  | awk 'BEGIN {FS = ":.*?## "} {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build the fsj binary into ./bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(PKG)

install: ## Install fsj into GOBIN (go install)
	go install -trimpath -ldflags "$(LDFLAGS)" $(PKG)

run: ## Build and run fsj (pass args with ARGS="score")
	go run $(PKG) $(ARGS)

test: ## Run the full test suite
	go test ./...

vet: ## Run go vet static analysis
	go vet ./...

fmt: ## Format all Go source in place
	gofmt -w $(FMT_DIRS)

fmt-check: ## Fail if any Go source is not gofmt-clean (CI gate)
	@unformatted="$$(gofmt -l $(FMT_DIRS))"; \
	if [ -n "$$unformatted" ]; then \
	  echo "These files are not gofmt-clean:"; echo "$$unformatted"; exit 1; \
	fi

lint: vet fmt-check ## Run vet + gofmt check (the CI quality gate)

check: lint test ## Run everything CI runs (lint + tests)

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)

build-all: ## Cross-build release binaries for darwin arm64 + amd64 into ./bin
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-darwin-arm64 $(PKG)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-darwin-amd64 $(PKG)

release: ## Tag vVERSION and push it (triggers release.yml). Usage: make release VERSION=X.Y.Z
	@echo "$(VERSION)" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$$' \
	  || { echo "usage: make release VERSION=X.Y.Z (got '$(VERSION)')" >&2; exit 1; }
	git tag -a v$(VERSION) -m "Release v$(VERSION)"
	git push origin v$(VERSION)
