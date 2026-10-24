BINARY := libreai
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO ?= go
GOAMD64 ?= v1
CGO_ENABLED ?= 0
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build build-linux-amd64 run test test-go test-js vet fmt clean check-binary deps deps-check

all: build

build:
	mkdir -p bin
	CGO_ENABLED=$(CGO_ENABLED) GOAMD64=$(GOAMD64) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/libreai

build-linux-amd64:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 ./cmd/libreai

run:
	CGO_ENABLED=$(CGO_ENABLED) GOAMD64=$(GOAMD64) $(GO) run ./cmd/libreai

deps:
	./scripts/install-deps.sh

deps-check:
	./scripts/install-deps.sh --check

test: test-go test-js

test-go:
	$(GO) test ./...

test-js:
	@if command -v node >/dev/null 2>&1; then node --test tests/frontend/*.test.mjs; else echo "node not found; skipping development-only frontend tests"; fi

vet:
	$(GO) vet ./...

fmt:
	gofmt -w $$(find cmd internal -name '*.go' -type f)

clean:
	rm -rf bin

SCAN_BINARY := bin/.libreai-scan
SCAN_DUMP := bin/.libreai-objdump.txt
SCAN_APP_DUMP := bin/.libreai-app-objdump.txt
SCAN_HITS := bin/.libreai-simd-hits.txt
SCAN_ALL_HITS := bin/.libreai-all-avx-hits.txt
SCAN_FMA_HITS := bin/.libreai-all-fma-hits.txt
SIMD_SCAN := scripts/scan-simd.awk
APP_IMPORTS := $(shell CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 $(GO) list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./cmd/libreai 2>/dev/null | sed -e 's/\./\\./g' | paste -sd'|' -)
APP_SYMBOLS := ^($(APP_IMPORTS)|main\.)

check-binary: build-linux-amd64
	@rm -f $(SCAN_BINARY) $(SCAN_DUMP) $(SCAN_APP_DUMP) $(SCAN_HITS) $(SCAN_ALL_HITS) $(SCAN_FMA_HITS)
	@echo "== file =="
	@file bin/$(BINARY)-linux-amd64
	@echo "== linked libraries (expect: not a dynamic executable) =="
	@ldd bin/$(BINARY)-linux-amd64 2>&1 || true
	@echo "== embedded build settings =="
	@go version -m bin/$(BINARY)-linux-amd64
	@echo "== rebuilding unstripped copy for instruction scan =="
	@test -n '$(APP_IMPORTS)' || { echo "ERROR: could not list application packages" >&2; exit 1; }
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 $(GO) build -trimpath -o $(SCAN_BINARY) ./cmd/libreai
	@$(GO) tool objdump $(SCAN_BINARY) > $(SCAN_DUMP)
	@test -s $(SCAN_DUMP) || { echo "ERROR: disassembly was empty" >&2; exit 1; }
	@$(GO) tool objdump -s '$(APP_SYMBOLS)' $(SCAN_BINARY) > $(SCAN_APP_DUMP)
	@test -s $(SCAN_APP_DUMP) || { echo "ERROR: application disassembly was empty" >&2; exit 1; }
	@grep -q '^TEXT main\.' $(SCAN_APP_DUMP) || { echo "ERROR: main package missing from application scan" >&2; exit 1; }
	@echo "== application and module code scan (expect no matches) =="
	@if awk -v mode=avx -f $(SIMD_SCAN) $(SCAN_APP_DUMP) > $(SCAN_HITS); then \
		echo "none found in application/module code"; \
	else \
		echo "ERROR: non-v1 SIMD instructions found in application/module code:" >&2; \
		head -20 $(SCAN_HITS) >&2; \
		exit 1; \
	fi
	@echo "== optional SIMD in Go runtime/stdlib (expected; selected by CPUID) =="
	@awk -v mode=avx -f $(SIMD_SCAN) $(SCAN_DUMP) > $(SCAN_ALL_HITS) || true
	@awk -v mode=fma -f $(SIMD_SCAN) $(SCAN_DUMP) > $(SCAN_FMA_HITS) || true
	@printf "  AVX/AVX2/AVX-512 instructions: %s in %s function(s)\n" "$$(wc -l < $(SCAN_ALL_HITS))" "$$(cut -f1 $(SCAN_ALL_HITS) | sort -u | wc -l)"
	@printf "  FMA instructions:              %s in %s function(s)\n" "$$(wc -l < $(SCAN_FMA_HITS))" "$$(cut -f1 $(SCAN_FMA_HITS) | sort -u | wc -l)"
	@echo "  functions containing optional SIMD (first 10):"
	@cut -f1 $(SCAN_ALL_HITS) | sort -u | head -10 | sed 's/^/    /'
	@echo "  These instructions are in runtime/stdlib code and are reached only"
	@echo "  through CPUID-dispatched fallbacks; x86-64-v1 CPUs never execute them."
	@rm -f $(SCAN_BINARY) $(SCAN_DUMP) $(SCAN_APP_DUMP) $(SCAN_HITS) $(SCAN_ALL_HITS) $(SCAN_FMA_HITS)
