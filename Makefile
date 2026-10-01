GO ?= go

.PHONY: build install test check deps demo demo-gif
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
demo: build
	bash demo/run.sh
demo-gif: build
	vhs validate demo/demo.tape
	vhs demo/demo.tape
