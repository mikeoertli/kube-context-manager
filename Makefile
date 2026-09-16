GO ?= go
VERSION := $(shell cat VERSION)

.PHONY: build install test check deps
build:
	$(GO) build -ldflags '-X main.version=$(VERSION)' -o bin/kcm ./cmd/kcm
install:
	$(GO) install -ldflags '-X main.version=$(VERSION)' ./cmd/kcm
deps:
	$(GO) mod tidy
test:
	$(GO) test ./...
check:
	test -z "$$($(GO) fmt ./...)"
	$(GO) vet ./...
	$(GO) test ./...
