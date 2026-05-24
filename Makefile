GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

BIN_DIR    := .
CLIENT_BIN := $(BIN_DIR)/anylan-client
SERVER_BIN := $(BIN_DIR)/anylan-server

ifeq ($(GOOS),windows)
CLIENT_BIN := $(CLIENT_BIN).exe
SERVER_BIN := $(SERVER_BIN).exe
endif

.PHONY: all build build-client build-server build-windows test clean

all: build

## build both binaries for the current platform
build: build-client build-server

build-client:
	go build -o $(CLIENT_BIN) ./cmd/client

build-server:
	go build -o $(SERVER_BIN) ./cmd/server

## cross-compile the client for Windows (amd64)
build-windows:
	GOOS=windows GOARCH=amd64 go build -o anylan-client.exe ./cmd/client

test:
	go test ./...

clean:
	rm -f anylan-client anylan-client.exe anylan-server anylan-server.exe
