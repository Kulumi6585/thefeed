.PHONY: all build build-server build-client test clean lint fmt vet

BINARY_SERVER = thefeed-server
BINARY_CLIENT = thefeed-client
BUILD_DIR = build

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -s -w \
	-X github.com/sartoopjj/thefeed/internal/version.Version=$(VERSION) \
	-X github.com/sartoopjj/thefeed/internal/version.Commit=$(COMMIT) \
	-X github.com/sartoopjj/thefeed/internal/version.Date=$(DATE)

GOFLAGS = -trimpath -ldflags="$(LDFLAGS)"
export CGO_ENABLED = 0

all: test build

build: build-server build-client

build-server:
	@mkdir -p $(BUILD_DIR)
	go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_SERVER) ./cmd/server

build-client:
	@mkdir -p $(BUILD_DIR)
	go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_CLIENT) ./cmd/client

test:
	go test -race -count=1 ./...

lint: vet
	@command -v golangci-lint >/dev/null 2>&1 || echo "golangci-lint not found, skipping"
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || true

vet:
	go vet ./...

fmt:
	gofmt -s -w .

clean:
	rm -rf $(BUILD_DIR)

# Cross-compilation targets
build-all: build-linux-amd64 build-linux-arm64 build-darwin-amd64 build-darwin-arm64 build-windows-amd64

build-linux-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_SERVER)-linux-amd64 ./cmd/server
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_CLIENT)-linux-amd64 ./cmd/client

build-linux-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_SERVER)-linux-arm64 ./cmd/server
	GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_CLIENT)-linux-arm64 ./cmd/client

build-darwin-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_SERVER)-darwin-amd64 ./cmd/server
	GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_CLIENT)-darwin-amd64 ./cmd/client

build-darwin-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_SERVER)-darwin-arm64 ./cmd/server
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_CLIENT)-darwin-arm64 ./cmd/client

build-windows-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_SERVER)-windows-amd64.exe ./cmd/server
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_CLIENT)-windows-amd64.exe ./cmd/client
