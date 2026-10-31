# go-meta — deterministic build orchestration.
#
# Ordering contract (blueprint §6): templ generation and route generation
# happen BEFORE the Go build; Vite compiles the client bundle. Node/npm is
# used only here, at build time — production runs the Go binary plus
# dist/client assets with no Node on the host.

GO ?= go
NPM ?= npm

.PHONY: help generate build test check clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-10s %s\n", $$1, $$2}'

generate: ## Run templ + route codegen (must precede go build)
	$(GO) tool templ generate
	$(GO) run ./cmd/routegen

build: generate ## Full production build: templ + Go binary + client assets
	$(GO) build -o bin/example ./cmd/example
	$(NPM) ci
	$(NPM) run build

test: generate ## Run all checks: Go tests/vet, svelte-check, tsc
	$(GO) vet ./...
	$(GO) test ./...
	$(NPM) run check
	$(NPM) exec tsc -- --noEmit

clean: ## Remove build outputs (bin, dist)
	rm -rf bin dist
