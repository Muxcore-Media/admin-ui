.PHONY: build test lint css css-watch dev run clean fmt tidy ci help templ

GO ?= go
TEMPL ?= $(shell which templ 2>/dev/null || echo ~/go/bin/templ)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")
LDFLAGS ?= -s -w -X main.version=$(VERSION)

TAILWIND_VERSION ?= v4.1.6
TAILWIND_BIN ?= ./tailwindcss

build: css templ
	$(GO) build -ldflags="$(LDFLAGS)" -o admin-ui ./cmd/module

templ:
	$(TEMPL) generate

css: $(TAILWIND_BIN) input.css
	$(TAILWIND_BIN) -i input.css -o assets/dist/styles.css --minify

css-watch: $(TAILWIND_BIN) input.css
	$(TAILWIND_BIN) -i input.css -o assets/dist/styles.css --watch

$(TAILWIND_BIN):
	@echo "downloading Tailwind CSS standalone CLI..."
	@if command -v curl >/dev/null 2>&1; then \
		curl -sL "https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-linux-x64" -o $(TAILWIND_BIN); \
	elif command -v wget >/dev/null 2>&1; then \
		wget -q "https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-linux-x64" -O $(TAILWIND_BIN); \
	else \
		echo "error: need curl or wget to download tailwindcss"; exit 1; \
	fi
	chmod +x $(TAILWIND_BIN)

test:
	$(GO) test -race -count=1 -timeout 15m ./...

GOLANGCI_LINT ?= $(shell which golangci-lint 2>/dev/null)

lint:
	@if [ -n "$(GOLANGCI_LINT)" ]; then \
		$(GOLANGCI_LINT) run --timeout 120s ./...; \
	else \
		$(GO) vet ./...; \
	fi

run: build
	./admin-ui

dev: css templ
	$(GO) run ./cmd/module

clean:
	rm -f admin-ui admin-ui.exe
	rm -rf assets/dist/

fmt:
	$(GO) fmt ./...
	$(TEMPL) fmt ./...

tidy:
	$(GO) mod tidy

ci: lint test build

help:
	@echo "Targets:"
	@echo "  build      - compile Tailwind CSS, generate Templ, build binary"
	@echo "  css        - compile Tailwind CSS"
	@echo "  css-watch  - compile Tailwind CSS in watch mode"
	@echo "  templ      - generate Templ Go code"
	@echo "  test       - run tests with race detection"
	@echo "  lint       - golangci-lint or go vet"
	@echo "  run        - build and run"
	@echo "  dev        - build CSS + Templ and run in dev mode"
	@echo "  clean      - remove build artifacts"
	@echo "  fmt        - format Go and Templ files"
	@echo "  tidy       - go mod tidy"
	@echo "  ci         - lint + test + build (CI pipeline)"
