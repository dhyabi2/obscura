# Obscura (OBX) — build & test
VERSION ?= 0.1.0-prototype
# AUDIT FIX: the DEFAULT build (no tags) ships the KAT-verified canonical RandomX
# PoW backend. The pure-Go `vm-randomx-style` prototype has near-zero memory-
# hardness and must never back a value-bearing node; select it ONLY with the
# explicit opt-in `make BUILDTAGS=protopow` for fast prototype/dev/test builds.
BUILDTAGS ?=
GOFLAGS := -trimpath -tags "$(BUILDTAGS)"
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build node wallet test test-short bench release run-node clean fmt vet tidy

all: build

build: node wallet ## build both binaries into bin/

node: ## build the full node + miner
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/obscura-node ./cmd/obscura-node

wallet: ## build the CLI wallet
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/obscura-wallet ./cmd/obscura-wallet

test: ## full test suite (includes 2048-bit class-group tests + RandomX KATs)
	go test -tags "$(BUILDTAGS)" ./... -timeout 300s

test-short: ## faster test run (skips heavy class-group tests)
	go test -tags "$(BUILDTAGS)" ./... -short -timeout 180s

test-proto: ## fast dev test run on the prototype PoW (insecure backend; never for release)
	go test -tags protopow ./... -short -timeout 180s

bench: ## run benchmarks
	go test -tags "$(BUILDTAGS)" ./pkg/... -run x -bench . -benchmem

release: ## cross-compile release archives into dist/
	VERSION=$(VERSION) ./build.sh

run-node: node ## build and run a mining node (testnet)
	./bin/obscura-node --mine

fmt: ; go fmt ./...
vet: ; go vet ./...
tidy: ; go mod tidy

clean: ; rm -rf bin dist

help: ## list targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

devnet: ## run the one-command 2-node devnet demo
	./scripts/devnet.sh

testnet: ## run an N-node local testnet (make testnet N=5)
	./scripts/testnet.sh $(N)
