SHELL := /bin/sh

GO ?= go

.PHONY: lint test build

lint:
	@set -e; \
	files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then \
		printf '%s\n' 'unformatted Go files:' >&2; \
		printf '%s\n' "$$files" >&2; \
		printf '%s\n' "run 'gofmt -w .' to format them" >&2; \
		exit 1; \
	fi

test:
	$(GO) test ./...

build:
	$(GO) build ./...
