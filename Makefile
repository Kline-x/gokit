GO ?= $(HOME)/sdk/go/bin/go

.PHONY: build test lint tidy example-test all

all: build lint test

build:
	$(GO) build ./...

test:
	$(GO) test -race ./...

lint:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

example-test:
	cd example/minimal && $(GO) test -race ./...
