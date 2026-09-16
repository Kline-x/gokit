# GO 指向要使用的 Go 工具链。默认取 PATH 上的 go；
# 若本机的 Go 不在 PATH 上，可以覆盖，例如：
#   make GO=$HOME/sdk/go/bin/go test
GO ?= go

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
