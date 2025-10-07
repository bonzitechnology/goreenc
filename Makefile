.PHONY: all build clean install test help run fmt vet build-linux build-windows build-macos build-all

# Build variables
BINARY_NAME=goreenc
BUILD_DIR=.
CMD_DIR=./cmd/goreenc
INSTALL_PATH=/usr/local/bin

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOFMT=$(GOCMD) fmt
GOVET=$(GOCMD) vet
GOMOD=$(GOCMD) mod

# Build flags
BUILD_FLAGS=-buildvcs=false
LDFLAGS=-ldflags="-s -w"

all: build

## build: Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	$(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_DIR)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

## build-debug: Build with debug symbols
build-debug:
	@echo "Building $(BINARY_NAME) with debug symbols..."
	$(GOBUILD) $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_DIR)
	@echo "Debug build complete: $(BUILD_DIR)/$(BINARY_NAME)"

## clean: Remove build artifacts
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	rm -f $(BUILD_DIR)/$(BINARY_NAME)
	rm -rf /tmp/goreenc
	@echo "Clean complete"

## install: Install binary to system
install: build
	@echo "Installing $(BINARY_NAME) to $(INSTALL_PATH)..."
	cp $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "Install complete"

## uninstall: Remove binary from system
uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	rm -f $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "Uninstall complete"

## test: Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

## fmt: Format code
fmt:
	@echo "Formatting code..."
	$(GOFMT) ./...

## vet: Run go vet
vet:
	@echo "Running go vet..."
	$(GOVET) ./...

## tidy: Tidy go modules
tidy:
	@echo "Tidying go modules..."
	$(GOMOD) tidy

## run: Build and run with dry-run
run: build
	@echo "Running $(BINARY_NAME) in dry-run mode..."
	./$(BINARY_NAME) .

## deps: Download dependencies
deps:
	@echo "Downloading dependencies..."
	$(GOGET) -v ./...
	$(GOMOD) download

## check: Run fmt, vet, and test
check: fmt vet test
	@echo "All checks passed"

## build-linux: Build for Linux (amd64)
build-linux:
	@echo "Building for Linux (amd64)..."
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 $(CMD_DIR)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64"

## build-linux-arm64: Build for Linux (arm64)
build-linux-arm64:
	@echo "Building for Linux (arm64)..."
	GOOS=linux GOARCH=arm64 $(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 $(CMD_DIR)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64"

## build-windows: Build for Windows (amd64)
build-windows:
	@echo "Building for Windows (amd64)..."
	GOOS=windows GOARCH=amd64 $(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe $(CMD_DIR)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe"

## build-macos: Build for macOS (amd64 and arm64)
build-macos:
	@echo "Building for macOS (amd64)..."
	GOOS=darwin GOARCH=amd64 $(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-macos-amd64 $(CMD_DIR)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)-macos-amd64"
	@echo "Building for macOS (arm64/Apple Silicon)..."
	GOOS=darwin GOARCH=arm64 $(GOBUILD) $(BUILD_FLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-macos-arm64 $(CMD_DIR)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)-macos-arm64"

## build-all: Build for all platforms
build-all: build-linux build-linux-arm64 build-windows build-macos
	@echo "All platform builds complete"

## help: Show this help
help:
	@echo "Available targets:"
	@echo ""
	@grep -E '^## ' Makefile | sed 's/## /  /'
	@echo ""
	@echo "Examples:"
	@echo "  make build       # Build the binary"
	@echo "  make install     # Install to /usr/local/bin"
	@echo "  make clean       # Remove build artifacts"
	@echo "  make check       # Run all checks"
