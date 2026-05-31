OUT_DIR := out

.PHONY: all build webui-build build-client build-server build-linux build-windows build-darwin test clean

all: build

## build both binaries for the current platform
build: build-client build-server

webui-build:
	npm --prefix webui ci
	npm --prefix webui run build

build-client: webui-build
	go build -o $(OUT_DIR)/anylan-client ./cmd/client

build-server: webui-build
	go build -o $(OUT_DIR)/anylan-server ./cmd/server

## cross-compile Linux client and server (amd64)
build-linux: webui-build
	GOOS=linux GOARCH=amd64 go build -o $(OUT_DIR)/anylan-client-linux-amd64 ./cmd/client
	GOOS=linux GOARCH=amd64 go build -o $(OUT_DIR)/anylan-server-linux-amd64 ./cmd/server

## cross-compile Windows binaries (amd64)
build-windows: webui-build
	GOOS=windows GOARCH=amd64 go build -o $(OUT_DIR)/anylan-client-windows-amd64.exe ./cmd/client
	GOOS=windows GOARCH=amd64 go build -o $(OUT_DIR)/anylan-server-windows-amd64.exe ./cmd/server

## cross-compile Darwin/macOS binaries (amd64 + arm64)
build-darwin: webui-build
	GOOS=darwin GOARCH=amd64 go build -o $(OUT_DIR)/anylan-client-darwin-amd64 ./cmd/client
	GOOS=darwin GOARCH=arm64 go build -o $(OUT_DIR)/anylan-client-darwin-arm64 ./cmd/client
	GOOS=darwin GOARCH=amd64 go build -o $(OUT_DIR)/anylan-server-darwin-amd64 ./cmd/server
	GOOS=darwin GOARCH=arm64 go build -o $(OUT_DIR)/anylan-server-darwin-arm64 ./cmd/server

test: webui-build
	go test ./...

clean:
	rm -rf $(OUT_DIR)
