# ======================================================================================
# Project Configuration
# ======================================================================================

# Project metadata
PROJECT_NAME := git-stack
DESCRIPTION  := Manage stacks of interdependent Git branches

# Directories
ROOT_DIR    := $(shell dirname $(realpath $(lastword $(MAKEFILE_LIST))))
DIST_DIR    := $(ROOT_DIR)/dist
INSTALL_DIR := $(HOME)/.local/bin

# Build flags
LDFLAGS := -s -w

# Colors
BLUE    := \033[34m
GREEN   := \033[32m
YELLOW  := \033[33m
RED     := \033[31m
MAGENTA := \033[35m
CYAN    := \033[36m
BOLD    := \033[1m
RESET   := \033[0m

# ======================================================================================
# @Linters
# ======================================================================================

.PHONY: lint
lint: tidy format lint-golangci ## Run all linters

.PHONY: tidy
tidy: ## Tidy up dependencies
	go mod tidy

.PHONY: format
format: ## Run code formatters
	golangci-lint fmt

.PHONY: lint-golangci
lint-golangci: ## Run golangci-lint
	golangci-lint run ./...

# ======================================================================================
# @Dependencies
# ======================================================================================

.PHONY: update
update: ## Update dependencies
	go get -u ./...

.PHONY: tools
tools: ## Install development tools
	go install tool

# ======================================================================================
# Directory Creation
# ======================================================================================

$(DIST_DIR):
	mkdir -p $@

# ======================================================================================
# @Build
# ======================================================================================

.PHONY: all
all: lint test build ## Run all checks and build

.PHONY: run
run: ## Run the main program
	go run ./cmd/git-stack/

.PHONY: build
build: $(DIST_DIR) ## Build the binary
	go build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/git-stack ./cmd/git-stack/

.PHONY: install
install: build ## Install the binary to $HOME/.local/bin
	mkdir -p $(INSTALL_DIR)
	cp $(DIST_DIR)/git-stack $(INSTALL_DIR)/git-stack

.PHONY: clean
clean: ## Clean the dist directory
	rm -rf $(DIST_DIR)

# ======================================================================================
# @Tests
# ======================================================================================

.PHONY: test
test: ## Run all tests
	go test -coverprofile=coverage.txt ./...

.PHONY: test-bench
test-bench: ## Run benchmarks
	go test -bench=. -benchmem ./...

# ======================================================================================
# @Help
# ======================================================================================

.PHONY: help
help: ## Display this help message
	@echo "$(CYAN)$(BOLD)$(PROJECT_NAME) - $(DESCRIPTION)$(RESET)"
	@awk 'BEGIN { FS = ":.*?##" } \
		/^[a-zA-Z0-9._-]+:.*?##/ { printf " $(CYAN)%-16s$(RESET) %s\n", $$1, $$2 } \
		/^# @/ { printf "\n$(MAGENTA)%s$(RESET)\n\n", substr($$0, 4) }' \
		$(MAKEFILE_LIST)
	@echo

.DEFAULT_GOAL := help
