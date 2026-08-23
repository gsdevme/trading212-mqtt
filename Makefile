BINARY := trading212-mqtt
BIN_DIR := bin
PKG     := ./cmd

GOLANGCI_LINT_VERSION := v2.12.2
GOLANGCI_LINT := $(BIN_DIR)/golangci-lint

.PHONY: build run run-mock test test-e2e lint

## build: compile the binary into ./bin
build:
	go build -o $(BIN_DIR)/$(BINARY) $(PKG)

## test: run unit + integration tests (excludes the godog features suite)
test:
	go test $(shell go list ./... | grep -v /features)

## test-e2e: run the godog acceptance suite
test-e2e:
	go test ./features/...

## run: build then run the poll -> MQTT service (respects MODE from .env)
run: build
	./$(BIN_DIR)/$(BINARY) serve

## run-mock: build then run the standalone mock Trading 212 API
run-mock: build
	./$(BIN_DIR)/$(BINARY) mock

$(GOLANGCI_LINT):
	GOBIN=$(abspath $(BIN_DIR)) go install \
	  github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

## lint: run golangci-lint (installs the pinned binary into ./bin on first use)
lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run
