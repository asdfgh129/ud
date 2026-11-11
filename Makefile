# Universal Downloader · P0 build entrypoints (docs/13-构建发布与运维.md §3)
# All transport builds MUST keep CGO_ENABLED=0 (ADR-0001 / NFR-PORT-01).
SHELL := /bin/bash
GO ?= go
PYTHON ?= python3
GOPROXY ?= https://proxy.golang.org,direct
export CGO_ENABLED := 0
export GOPROXY
# go.mod pins Go 1.27.4 (security fixes GO-2026-6809, GO-2026-6859). GOTOOLCHAIN=auto must be
# able to verify/download the toolchain module; a global `GOSUMDB=off` breaks
# that with "checksum database disabled". CI uses the same default sumdb.
export GOSUMDB ?= sum.golang.org

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo v0.1.0)
BUILD_ID ?= $(shell date -u +%Y%m%dT%H%M%SZ)
LDFLAGS := -s -w -X github.com/asdfgh129/ud/internal/version.Version=$(VERSION) \
           -X github.com/asdfgh129/ud/internal/version.BuildID=$(BUILD_ID)
DIST := dist

PLATFORMS := \
	darwin/amd64 darwin/arm64 \
	linux/amd64 linux/arm64 \
	windows/amd64 windows/arm64

.PHONY: all gen lint lint-arch test build docs sbom licenses cover vuln bench rollback-drill gates interop interop-gate sign package release check clean fmt vet

all: lint test build docs

gen:
	$(GO) generate ./...

fmt:
	@test -z "$$(gofmt -l ./cmd ./internal)" || { echo "gofmt needed on:"; gofmt -l ./cmd ./internal; exit 1; }

vet:
	$(GO) vet ./...

# Static architecture guards required by docs/15 §2 and docs/13 §4.
lint-arch:
	$(PYTHON) tools/check_arch.py

lint: fmt vet lint-arch
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; else \
	  echo "  [skip] golangci-lint not installed; gofmt/go vet/arch checks passed"; fi

test:
	$(GO) test ./... -count=1

# Six-platform CGO_ENABLED=0 matrix (NFR-PORT-01 / P0 acceptance).
build:
	@mkdir -p $(DIST)
	@set -e; for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=""; if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
	  echo "  build $$os/$$arch"; \
	  GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
	    -o $(DIST)/udd-$$os-$$arch$$ext ./cmd/udd; \
	  GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
	    -o $(DIST)/udctl-$$os-$$arch$$ext ./cmd/udctl; \
	done
	@echo "  CGO_ENABLED=0 six-platform build OK"

docs:
	$(PYTHON) tools/build_docs.py

# P0 milestone review gates (docs/12 §8, docs/14 §8). All evidence lands in
# dist/ and is gitignored; results are summarised in dist/GATES.md.
cover:
	$(PYTHON) tools/gates.py cover

sbom:
	$(PYTHON) tools/gates.py sbom

licenses:
	$(PYTHON) tools/gates.py licenses

vuln:
	$(PYTHON) tools/gates.py vuln

bench:
	$(PYTHON) tools/gates.py bench

rollback-drill:
	$(PYTHON) tools/gates.py rollback

gates:
	$(PYTHON) tools/gates.py all
	@echo "  see dist/GATES.md and dist/gates.json"

# CI gate: any WARN (coverage below threshold, missing vuln scanner) is a failure.
gates-strict:
	$(PYTHON) tools/gates.py all --strict
	@echo "  see dist/GATES.md and dist/gates.json"

# WBS 11 interoperability suite v1. `make interop` runs the automatable
# loopback cases and writes dist/interop.json + dist/INTEROP.md. It does NOT
# claim the L4 release gate is met: SKIPPED external cases are reported as such.
interop:
	$(GO) run ./cmd/udinterop --out $(DIST)

# Release gate: exits non-zero unless BT-01..BT-14 are all PASS. Never wire
# this into CI until a lab with real clients / packet capture / gigabit LAN
# exists; running it here must fail loudly rather than pretend to pass.
interop-gate:
	$(GO) run ./cmd/udinterop --out $(DIST) --release-gate-only

sign package release:
	@echo "  [P0] '$@' is a release-stage target (docs/13 §3)"; exit 0

check: lint test docs

clean:
	rm -rf $(DIST)
