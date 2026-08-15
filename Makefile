# Repo-level checks. Every target here is what CI runs — if it passes locally it passes there.
#
#   make setup    once per machine: tools, deps, venv
#   make check    everything, in the order a reviewer would want it
#   make fmt      fix what is fixable
#
# Go targets run inside backend/. Prettier and the fixture validator run at the
# root, because they cover markdown and JSON that live outside backend/.

SHELL   := /usr/bin/env bash
VENV    := .venv
PYTHON  := $(VENV)/bin/python
PIP     := $(VENV)/bin/pip
BACKEND := backend
TOOLS   := $(CURDIR)/.tools

# Pinned. A linter that changes version underneath you turns a clean branch red
# for reasons that have nothing to do with the branch.
GOLANGCI_VERSION := v2.6.1
MOCKERY_VERSION  := v3.5.1

export GOBIN := $(TOOLS)
export PATH  := $(TOOLS):$(PATH)

.PHONY: setup check fmt fmt-check lint \
        go-fmt go-fmt-check go-lint go-vet go-test go-tidy-check mocks \
        validate-fixtures e2e clean

# --- setup -------------------------------------------------------------------

setup: $(VENV)/.installed $(TOOLS)/golangci-lint $(TOOLS)/goimports
	npm install
	cd $(BACKEND) && go mod download

$(VENV)/.installed: scripts/requirements.txt
	python3 -m venv $(VENV)
	$(PIP) install --quiet --upgrade pip
	$(PIP) install --quiet -r scripts/requirements.txt
	touch $@

$(TOOLS)/golangci-lint:
	@mkdir -p $(TOOLS)
	cd $(BACKEND) && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

$(TOOLS)/goimports:
	@mkdir -p $(TOOLS)
	cd $(BACKEND) && go install golang.org/x/tools/cmd/goimports@latest

$(TOOLS)/mockery:
	@mkdir -p $(TOOLS)
	cd $(BACKEND) && go install github.com/vektra/mockery/v3@$(MOCKERY_VERSION)

# --- formatting --------------------------------------------------------------

fmt: go-fmt
	npx prettier --write .

fmt-check: go-fmt-check
	npx prettier --check .

# gofmt plus import grouping. A PostToolUse hook already runs gofmt on write,
# so this is the check that catches anything written another way.
go-fmt: $(TOOLS)/goimports
	cd $(BACKEND) && gofmt -w . && goimports -w .

go-fmt-check: $(TOOLS)/goimports
	@cd $(BACKEND) && \
	  unformatted=$$(gofmt -l . ; goimports -l .) ; \
	  if [[ -n "$$unformatted" ]]; then \
	    echo "not gofmt/goimports clean:" ; echo "$$unformatted" ; exit 1 ; \
	  fi

# --- linting -----------------------------------------------------------------

lint: go-vet go-lint go-tidy-check

go-vet:
	cd $(BACKEND) && go vet ./...

# The layering rules from CLAUDE.md are enforced here by depguard, not by
# memory: internal/domain may import nothing from internal/, and internal/port
# may not name pgx, chi, or a model vendor.
go-lint: $(TOOLS)/golangci-lint
	cd $(BACKEND) && golangci-lint run ./...

# A go.mod that drifts from the imports is a build that works only on the
# machine that has the module cache warm.
go-tidy-check:
	@cd $(BACKEND) && \
	  cp go.mod go.mod.bak && cp go.sum go.sum.bak && \
	  go mod tidy && \
	  if ! diff -q go.mod go.mod.bak >/dev/null || ! diff -q go.sum go.sum.bak >/dev/null; then \
	    mv go.mod.bak go.mod ; mv go.sum.bak go.sum ; \
	    echo "go.mod/go.sum are not tidy — run: cd backend && go mod tidy" ; exit 1 ; \
	  fi ; \
	  rm -f go.mod.bak go.sum.bak

# --- tests -------------------------------------------------------------------

# Unit tests only. The integration suite needs Docker and has its own target.
go-test:
	cd $(BACKEND) && go test ./... -short -count=1

# Mocks are generated from the port interfaces, never hand-written. Unit tests
# mock the layer below: controller tests mock services, service tests mock
# repositories.
mocks: $(TOOLS)/mockery
	cd $(BACKEND) && mockery

# Starts Postgres, applies the schema, runs every fixture, tears down.
e2e:
	$(BACKEND)/scripts/e2e.sh

# --- fixtures ----------------------------------------------------------------

# Structure against fixture.schema.json, plus the cross-file properties a schema
# cannot express — seed names, principal keys, {{capture}} resolution, and the
# ADR-0008 response-shape contract.
validate-fixtures: $(VENV)/.installed
	$(PYTHON) scripts/validate_fixtures.py

# --- everything ---------------------------------------------------------------

check: fmt-check lint validate-fixtures go-test

clean:
	rm -rf $(VENV) node_modules $(TOOLS)
