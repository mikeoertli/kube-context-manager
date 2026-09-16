GO ?= go

.PHONY: build install test check deps
build:
	$(GO) build -o bin/kcm ./cmd/kcm
install:
	$(GO) install ./cmd/kcm
deps:
	$(GO) mod tidy
test:
	$(GO) test ./...
check:
	test -z "$$($(GO) fmt ./...)"
	$(GO) vet ./...
	$(GO) test ./...
