# rmq-vertical-scaler — Go build tooling.
# Run `make help` to list targets. This is the Go equivalent of package.json "scripts".

BINARY   := rmq-vertical-scaler
REGISTRY ?= ferterahadi
IMAGE    := $(REGISTRY)/$(BINARY)
VERSION  ?= dev
LDFLAGS  := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := help
.PHONY: help build run-generate test cover vet fmt tidy image bench clean helm-lint

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## Compile a stripped static binary into dist/
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o dist/$(BINARY) ./cmd/$(BINARY)

run-generate: ## Run the manifest generator (pass ARGS="--config ...")
	go run ./cmd/$(BINARY) generate $(ARGS)

test: ## Run all unit tests (incl. the v1 YAML parity golden test)
	go test ./...

cover: ## Run tests with a coverage summary
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

vet: ## Run go vet
	go vet ./...

fmt: ## Format all Go files
	gofmt -w .

tidy: ## Sync go.mod/go.sum
	go mod tidy

image: ## Build the container image (requires Docker)
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

bench: ## Run the footprint benchmark harness (see research/benchmark.md)
	./scripts/benchmark.sh

clean: ## Remove build output
	rm -rf dist coverage.out

helm-lint: ## Lint + template-render the Helm chart (requires helm)
	helm lint charts/rmq-vertical-scaler
	helm template rvs charts/rmq-vertical-scaler > /dev/null
	@if helm template rvs charts/rmq-vertical-scaler --set pdb.enabled=false | grep -q PodDisruptionBudget; then \
		echo "pdb.enabled=false still renders a PDB" && exit 1; fi
	@echo "helm chart OK"
