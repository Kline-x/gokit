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

# PROTOC 与插件的位置。默认取 PATH 上的，可覆盖。
PROTOC ?= protoc
PROTO_DIR ?= example/minimal/api

.PHONY: tools proto

# tools 安装代码生成需要的 protoc 插件。
#
# 版本必须与 go.mod 里锁定的模块对齐：生成器比运行时新是 protobuf 官方
# 明确不支持的组合，今天能编过不代表明天还能。用 @latest 会让不同机器
# 生成出不同的代码。
tools:
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@v1.35.2
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1

# proto 根据 idl 生成 Go 代码。生成物与 .proto 同目录。
proto:
	$(PROTOC) --proto_path=$(PROTO_DIR) \
		--go_out=$(PROTO_DIR) --go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_DIR) --go-grpc_opt=paths=source_relative \
		$(shell find $(PROTO_DIR) -name '*.proto')
