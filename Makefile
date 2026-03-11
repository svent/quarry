GO := go
CGO_ENABLED := 0
GOFLAGS := -trimpath
LDFLAGS := -s -w

BIN_DIR := bin

BINARIES := chat ingest server
SWIFT_BINARIES := menubar

.PHONY: all clean test vet fmt $(BINARIES) $(SWIFT_BINARIES)

all: $(BINARIES) $(SWIFT_BINARIES)

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

chat: $(BIN_DIR)
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/chat ./cmd/chat

ingest: $(BIN_DIR)
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/ingest ./cmd/ingest

server: $(BIN_DIR)
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/server ./cmd/server

menubar: $(BIN_DIR)
	swift build -c release --arch arm64 --arch x86_64 --package-path macos/MenuBarChat
	cp macos/MenuBarChat/.build/apple/Products/Release/MenuBarChat $(BIN_DIR)/MenuBarChat

test:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) test ./...

vet:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) vet ./...

fmt:
	$(GO) fmt ./...

clean:
	rm -rf $(BIN_DIR)
