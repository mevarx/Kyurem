# Kyurem — common development commands.
# Go module targets assume you run these from the repository root.

GO      ?= go
GATEWAY := ./gateway

.PHONY: help build test vet fmt run-demo clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## Build the gateway binary
	$(GO) build -o bin/kyurem $(GATEWAY)/cmd/kyurem

test: ## Run gateway tests with the race detector
	$(GO) test -race $(GATEWAY)/...

vet: ## Static analysis
	$(GO) vet $(GATEWAY)/...

fmt: ## Format Go sources
	$(GO) fmt $(GATEWAY)/...

run-demo: ## Start the M1 demo (gateway + two agents) locally
	bash demo/scenarios/m1-basic.sh

clean: ## Remove build artifacts
	rm -rf bin
