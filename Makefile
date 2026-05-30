OUT_DIR := out

.PHONY: all build webui-build build-client build-server build-windows test clean

all: build

## build both binaries for the current platform
build: webui-build build-client build-server

webui-build:
	npm --prefix webui ci
	npm --prefix webui run build

build-client:
	go build -o $(OUT_DIR)/anylan-client ./cmd/client

build-server:
	go build -o $(OUT_DIR)/anylan-server ./cmd/server

## cross-compile the client for Windows (amd64)
build-windows:
	GOOS=windows GOARCH=amd64 go build -o $(OUT_DIR)/anylan-client.exe ./cmd/client

test:
	go test ./...

clean:
	rm -rf $(OUT_DIR)
