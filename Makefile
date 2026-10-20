# Kyurem — common development commands.
# Go module targets assume you run these from the repository root.

GO      ?= go
GATEWAY := ./gateway
DEMO    := ./demo
DASH    := ./dashboard
COMPOSE := docker compose -f deploy/compose/docker-compose.yml

.PHONY: help build build-demo test test-demo vet fmt run-demo demo-m2 demo-m3 \
        validate compose-up compose-down dashboard-install dashboard-check \
        dashboard-build clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

build: ## Build the gateway binary
	$(GO) build -o bin/kyurem $(GATEWAY)/cmd/kyurem

build-demo: ## Build the demo agents and client
	mkdir -p demo/bin
	$(GO) build -o demo/bin/go-echo     $(DEMO)/agents/go-echo
	$(GO) build -o demo/bin/go-upper    $(DEMO)/agents/go-upper
	$(GO) build -o demo/bin/demo-client $(DEMO)/scenarios/client

test: ## Run gateway tests with the race detector
	$(GO) test -race $(GATEWAY)/...

test-demo: ## Run demo module tests with the race detector
	$(GO) test -race $(DEMO)/...

vet: ## Static analysis
	$(GO) vet $(GATEWAY)/...
	$(GO) vet $(DEMO)/...

fmt: ## Format Go sources
	$(GO) fmt $(GATEWAY)/...
	$(GO) fmt $(DEMO)/...

validate: ## Validate the shipped configuration files
	$(GO) run $(GATEWAY)/cmd/kyurem validate --config config/kyurem.example.yaml
	$(GO) run $(GATEWAY)/cmd/kyurem validate --config config/kyurem.compose.yaml

run-demo: ## Start the M1 demo (gateway + two agents) locally
	bash demo/scenarios/m1-basic.sh

demo-m2: ## Start the M2 demo (compose stack) and assert one task = one trace tree
	bash demo/scenarios/m2-tracing.sh

demo-m3: ## Run the M3 demo: a scripted failure is diagnosable through the management API
	bash demo/scenarios/m3-dashboard.sh

compose-up: ## Build and start the M2 compose stack
	$(COMPOSE) up --build -d

compose-down: ## Stop the compose stack and drop its volumes
	$(COMPOSE) down -v

dashboard-install: ## Install the dashboard dependencies
	cd $(DASH) && npm ci

dashboard-check: ## Typecheck, lint and test the dashboard
	cd $(DASH) && npm run gen:api && npm run typecheck && npm run lint && npm test

dashboard-build: ## Build the dashboard for production
	cd $(DASH) && npm run build

clean: ## Remove build artifacts
	rm -rf bin demo/bin $(DASH)/dist

