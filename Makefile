# HELP
# This will output the help for each task
# thanks to https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html

.DEFAULT_GOAL := help

.PHONY: tlsccm-upstream help all build fmt fmt-check lint lint-ci vet vuln test stress coverage cover fuzz check clean interop test-interop test-tlsccm e2e features nodeps download schemas generate generate-verify generate-check check-updates
help: ## This help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Parent otfabric/go.work does not list this module; isolate toolchain commands.
export GOWORK := off

# CI formats and lints with the Go version from go.mod, not with the toolchain installed here.
# gofmt's output changes between Go releases, so formatting is done with that version, and
# `fmt-check` also requires the local gofmt to agree.
GO_MOD_VERSION := $(shell sed -nE 's/^go ([0-9]+\.[0-9]+).*/\1/p' go.mod | head -n1)
GOFMT_CI = $(shell GOTOOLCHAIN=go$(GO_MOD_VERSION).0 go env GOROOT)/bin/gofmt

# Output directory for generated binaries
BIN_DIR := bin
# Library packages: tests and coverage (exclude examples and test helpers)
TEST_PKGS := $(shell go list ./... | grep -v '/examples')
# Coverage is measured over the library packages, whichever package's tests
# exercise them: not the example builders and programs (/examples), the
# generator's command (internal/gen), or the test-only e2e suite. The nested
# modules tlsccm and interop are not part of ./... .
COVER_PKGS := $(shell go list ./... | grep -v '/examples' | grep -v '/internal/gen$$' | grep -v '/e2e$$' | paste -sd, -)
# The statement coverage make coverage-gate (and CI) requires, in percent.
COVER_MIN ?= 90

all: build ## Default target: build the examples

build: ## Build the examples into ./bin
	@echo "Building examples"
	@mkdir -p $(BIN_DIR)
	@for dir in examples/*/; do \
		[ -f "$$dir/go.mod" ] && continue; \
		name="$$(basename "$$dir")"; \
		go build -o "$(BIN_DIR)/example-$$name" "./$$dir"; \
	done

# The Go files of the repository: tracked or new, not ignored. Not "." with
# its subdirectories, which may hold other checkouts (git worktrees).
GO_FILES = $(shell git ls-files --cached --others --exclude-standard -- '*.go')

fmt: ## Format Go code with the gofmt of the Go version in go.mod (what CI uses)
	@echo "Running gofmt (go$(GO_MOD_VERSION))"
	@$(GOFMT_CI) -w $(GO_FILES)

fmt-check: ## Fail if the go.mod-version gofmt or the local gofmt would change any file
	@echo "Checking formatting (gofmt go$(GO_MOD_VERSION) and local $$(go env GOVERSION))"
	@ci="$$($(GOFMT_CI) -l $(GO_FILES))"; loc="$$(gofmt -l $(GO_FILES))"; \
	if [ -n "$$ci$$loc" ]; then \
		[ -z "$$ci" ] || { echo "Not formatted for gofmt go$(GO_MOD_VERSION) (CI):"; echo "$$ci"; }; \
		[ -z "$$loc" ] || { echo "Not formatted for the local gofmt:"; echo "$$loc"; }; \
		echo "Files must be stable under both. Run 'make fmt'."; \
		exit 1; \
	fi

lint: ## Run staticcheck
	@echo "Running staticcheck"
	@staticcheck ./...

lint-ci: ## Run golangci-lint (see .golangci.yml)
	@echo "Running golangci-lint"
	@golangci-lint run ./...

vet: ## Run go vet
	@echo "Running go vet"
	@go vet ./...

vuln: ## Run govulncheck
	@echo "Running govulncheck"
	@govulncheck ./...

e2e: ## Run the end-to-end tests of this module's client against this module's server
	@echo "Running end-to-end tests"
	@go test -count=1 -race ./e2e/...

features: ## Check docs/FEATURES.md against the e2e and interop tests
	@go run scripts/features.go

nodeps: ## Fail if the module requires anything outside the standard library
	@echo "Checking that the module has no dependencies"
	@deps="$$(go list -m all | tail -n +2)"; \
	if [ -n "$$deps" ]; then echo "go.mod requires:"; echo "$$deps"; exit 1; fi

test: ## Run the library tests with the race detector
	@echo "Running tests"
	@go test -count=1 -race $(TEST_PKGS)

stress: ## Run the library tests shuffled, twice, with the race detector (CI)
	@echo "Running the tests shuffled, twice, with the race detector"
	@go test -count=2 -race -shuffle=on -timeout=30m $(TEST_PKGS)

coverage: ## Run tests with coverage on the library packages (writes coverage.out)
	@echo "Running coverage"
	@go test -count=1 -race -coverprofile=coverage.out -covermode=atomic -coverpkg=$(COVER_PKGS) $(TEST_PKGS)
	@go tool cover -func=coverage.out | tail -n1

coverage-gate: coverage ## Fail if the coverage of the library packages is below COVER_MIN percent
	@total=$$(go tool cover -func=coverage.out | awk '/^total:/ {sub("%", "", $$3); print $$3}'); \
	if awk -v t="$$total" -v m="$(COVER_MIN)" 'BEGIN { exit !(t + 0 >= m + 0) }'; then \
		echo "Coverage $$total% meets the gate of $(COVER_MIN)%"; \
	else \
		echo "Coverage $$total% is below the gate of $(COVER_MIN)%"; exit 1; \
	fi

cover: coverage ## Open coverage report in browser
	@echo "Opening coverage report"
	@go tool cover -html=coverage.out

# Duration each fuzz target runs. Override with FUZZTIME=... (e.g. FUZZTIME=5m).
FUZZTIME ?= 30s
# The fuzz targets, as <package>:<target>.
FUZZ_TARGETS := ./server:FuzzHandler ./security:FuzzHardwareModuleName ./schedule:FuzzResolve ./internal/dnsmsg:FuzzParse \
	.:FuzzDecode ./csipaus:FuzzDecode ./client:FuzzResolve

fuzz: ## Run the fuzz targets for FUZZTIME each (default 30s)
	@echo "Fuzzing (FUZZTIME=$(FUZZTIME))"
	@for t in $(FUZZ_TARGETS); do \
		pkg=$${t%%:*}; name=$${t##*:}; \
		echo "$$pkg $$name"; \
		go test -run '^$$' -fuzz "^$$name$$" -fuzztime $(FUZZTIME) "$$pkg" || exit 1; \
	done

test-interop: ## Run the client and the server against the ieee20305-interop images, pinned in interop/images.go (the interop module; needs Docker; INTEROP_TESTFLAGS=-v for every test)
	@echo "Running interop tests"
	@cd interop && go vet ./... && staticcheck ./... && golangci-lint run ./... && go test -race -count=1 -timeout=30m $(INTEROP_TESTFLAGS) ./...

interop: test-interop ## Alias for test-interop

tlsccm-upstream: ## Check tlsccm's copy of crypto/tls against Go 1.22.0 and Go's crypto/tls vulnerabilities (network)
	@cd tlsccm && go build -o ../bin/tlsccm-upstream ./internal/upstream && ../bin/tlsccm-upstream

test-tlsccm: ## Vet, lint and test the tlsccm module (CCM_8, Go 1.26.8 and dependencies of its own)
	@echo "Testing the tlsccm module"
	@cd tlsccm && go vet ./... && staticcheck $$(go list ./... | grep -v /internal/) && golangci-lint run ./... && govulncheck ./... && go test -count=1 -race ./...

check: fmt fmt-check nodeps features generate-verify lint lint-ci vet vuln test coverage-gate test-tlsccm ## Run format + generated-code check + lint + vet + vuln + test + coverage gate + the tlsccm module

# ── Code generation ──────────────────────────────────────────────────────────

# The upstream IEEE 2030.5 and CSIP-AUS schemas are pinned (URL and SHA-256)
# in schemas/manifest.json and downloaded into schemas/upstream/, which is not
# committed. The *_gen.go files generated from them are committed.
GEN := go run ./internal/gen

download: ## Download the pinned upstream schemas into schemas/upstream/ and verify their checksums
	@$(GEN) fetch

schemas: download ## Alias for download

generate: download ## Regenerate every *_gen.go file from the upstream schemas
	@$(GEN) generate

generate-verify: ## Check offline that the generated files record the sources pinned in schemas/manifest.json
	@$(GEN) verify

generate-check: generate ## Fail if regenerating changes the committed generated code (CI drift check)
	@git diff --exit-code --stat -- '*_gen.go' || { echo "Generated code is out of date: run make generate and commit the result."; exit 1; }
	@untracked="$$(git ls-files --others --exclude-standard -- '*_gen.go')"; \
	if [ -n "$$untracked" ]; then echo "Generated files are not committed:"; echo "$$untracked"; exit 1; fi

check-updates: ## Report upstream schema changes and new releases (exit status 3 when there are some)
	@$(GEN) check-updates

clean: ## Remove generated binaries and coverage artifacts
	@echo "Cleaning up"
	@rm -rf $(BIN_DIR) coverage.out coverage.html
