# Makefile — developer + CI entry points for the coding-standards §1 gate.
# `make check` runs exactly what CI runs. Requires $(go env GOPATH)/bin on PATH
# (run `make tools` once to install goimports, govulncheck, golangci-lint).

GO ?= go
GOLANGCI_VERSION ?= v2.12.2

.PHONY: all help check ci tools fmt fmt-check vet lint vulncheck build test test-race tidy tidy-check

all: check

help: ## show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

check: fmt-check vet lint vulncheck build test-race ## run the full standards §1 gate (CI runs this)

ci: check ## alias for check

tools: ## install goimports, govulncheck, golangci-lint
	$(GO) install golang.org/x/tools/cmd/goimports@latest
	$(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

fmt: ## format code in place (gofmt + goimports)
	gofmt -w .
	goimports -w .

fmt-check: ## fail if any file is not gofmt/goimports-clean
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needs to run on:"; echo "$$out"; exit 1; fi
	@out="$$(goimports -l .)"; if [ -n "$$out" ]; then echo "goimports needs to run on:"; echo "$$out"; exit 1; fi

vet: ## go vet
	$(GO) vet ./...

lint: ## golangci-lint
	golangci-lint run ./...

vulncheck: ## known vulnerabilities in called code paths
	govulncheck ./...

build: ## compile everything
	$(GO) build ./...

test: ## unit tests
	$(GO) test ./...

test-race: ## unit tests under the race detector
	$(GO) test -race ./...

tidy: ## sync go.mod / go.sum
	$(GO) mod tidy

tidy-check: ## fail if go.mod/go.sum are not tidy
	$(GO) mod tidy
	@git diff --exit-code go.mod go.sum || (echo "go.mod/go.sum not tidy — run 'make tidy'"; exit 1)
