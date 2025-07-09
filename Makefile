.PHONY: build clean test run-dev install-deps init-db check-connections run-example version release

BINARY_NAME=freescout-notifier
BUILD_DIR=build

# Version information
VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u '+%Y-%m-%d_%H:%M:%S')
GO_VERSION ?= $(shell go version | sed 's/go version //')

# If there's a VERSION file, use that instead
ifneq (,$(wildcard ./VERSION))
    VERSION = $(shell cat VERSION)
endif

# Build flags
LDFLAGS = -ldflags "\
    -X 'main.Version=$(VERSION)' \
    -X 'main.GitCommit=$(GIT_COMMIT)' \
    -X 'main.BuildDate=$(BUILD_DATE)' \
    -X 'main.GoVersion=$(GO_VERSION)'"

# Default target
all: build

# Build the binary with version information
build:
	@echo "Building $(BINARY_NAME) version $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) main.go
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

# Build for multiple platforms
build-all: build-linux build-darwin build-windows

build-linux:
	@echo "Building for Linux..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 main.go

build-darwin:
	@echo "Building for macOS..."
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 main.go
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 main.go

build-windows:
	@echo "Building for Windows..."
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe main.go

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)

# Run tests
test:
	@echo "Running tests..."
	go test ./...

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Development run with dry-run and verbose flags
run-dev:
	@echo "Running in development mode..."
	go run $(LDFLAGS) main.go --dry-run --verbose

# Install dependencies
install-deps:
	@echo "Installing dependencies..."
	go mod download
	go mod tidy

# Initialize the database
init-db:
	@echo "Initializing database..."
	$(BUILD_DIR)/$(BINARY_NAME) --init-db

# Check connections
check-connections:
	@echo "Checking connections..."
	$(BUILD_DIR)/$(BINARY_NAME) --check-connections

# Display version information
version:
	@echo "Version: $(VERSION)"
	@echo "Git Commit: $(GIT_COMMIT)"
	@echo "Build Date: $(BUILD_DATE)"
	@echo "Go Version: $(GO_VERSION)"

# Show version from built binary
version-binary:
	@if [ -f "$(BUILD_DIR)/$(BINARY_NAME)" ]; then \
		$(BUILD_DIR)/$(BINARY_NAME) --version; \
	else \
		echo "Binary not found. Run 'make build' first."; \
	fi

# Run with example flags
run-example:
	$(BUILD_DIR)/$(BINARY_NAME) \
		--freescout-dsn="readonly:password@tcp(localhost:3306)/freescout?parseTime=true&timeout=30s" \
		--freescout-url=https://support.example.com \
		--slack-webhook=https://hooks.slack.com/services/YOUR/WEBHOOK/URL \
		--dry-run \
		--verbose

# Create a release build (optimized)
release:
	@echo "Creating release build..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -a -installsuffix cgo $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) main.go
	@echo "Release build complete: $(BUILD_DIR)/$(BINARY_NAME)"

# Create a release with all platforms
release-all: clean
	@echo "Creating release builds for all platforms..."
	@$(MAKE) build-all
	@echo "Creating release archives..."
	@cd $(BUILD_DIR) && \
	for binary in $(BINARY_NAME)-*; do \
		if [ -f "$$binary" ]; then \
			echo "Creating archive for $$binary..."; \
			tar -czf "$$binary.tar.gz" "$$binary"; \
		fi \
	done
	@echo "Release archives created in $(BUILD_DIR)/"

# Docker build (if you want to add Docker support later)
docker-build:
	@echo "Building Docker image..."
	docker build -t $(BINARY_NAME):$(VERSION) .
	docker tag $(BINARY_NAME):$(VERSION) $(BINARY_NAME):latest

# Install the binary to GOPATH/bin or /usr/local/bin
install: build
	@echo "Installing $(BINARY_NAME)..."
	@if [ -n "$(GOPATH)" ]; then \
		cp $(BUILD_DIR)/$(BINARY_NAME) $(GOPATH)/bin/; \
		echo "Installed to $(GOPATH)/bin/$(BINARY_NAME)"; \
	else \
		sudo cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/; \
		echo "Installed to /usr/local/bin/$(BINARY_NAME)"; \
	fi

# Uninstall the binary
uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	@if [ -n "$(GOPATH)" ] && [ -f "$(GOPATH)/bin/$(BINARY_NAME)" ]; then \
		rm $(GOPATH)/bin/$(BINARY_NAME); \
		echo "Removed from $(GOPATH)/bin/$(BINARY_NAME)"; \
	elif [ -f "/usr/local/bin/$(BINARY_NAME)" ]; then \
		sudo rm /usr/local/bin/$(BINARY_NAME); \
		echo "Removed from /usr/local/bin/$(BINARY_NAME)"; \
	else \
		echo "$(BINARY_NAME) not found in PATH"; \
	fi

# Show help
help:
	@echo "Available targets:"
	@echo "  build           - Build the binary with version information"
	@echo "  build-all       - Build for all supported platforms"
	@echo "  build-linux     - Build for Linux"
	@echo "  build-darwin    - Build for macOS"
	@echo "  build-windows   - Build for Windows"
	@echo "  clean           - Remove build artifacts"
	@echo "  test            - Run tests"
	@echo "  test-coverage   - Run tests with coverage report"
	@echo "  run-dev         - Run in development mode"
	@echo "  install-deps    - Install Go dependencies"
	@echo "  init-db         - Initialize the database"
	@echo "  check-connections - Test connections"
	@echo "  version         - Show version information"
	@echo "  version-binary  - Show version from built binary"
	@echo "  run-example     - Run with example configuration"
	@echo "  release         - Create optimized release build"
	@echo "  release-all     - Create release builds for all platforms"
	@echo "  docker-build    - Build Docker image"
	@echo "  install         - Install binary to system PATH"
	@echo "  uninstall       - Remove binary from system PATH"
	@echo "  help            - Show this help message"
	@echo ""
	@echo "Version: $(VERSION)"
	@echo "Git Commit: $(GIT_COMMIT)"
