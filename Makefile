.PHONY: build clean test install help

# Variables
BINARY_NAME=elastic-ci-executor
VERSION?=DEV
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-X 'main._VERSION=$(VERSION)' \
        -X 'main._BUILD_TIME=$(BUILD_TIME)'

# Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p bin
	@go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY_NAME) ./cmd/executor
	@echo "Done."

# Run tests
test:
	@echo "Running tests..."
	go test -v ./...

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -f $(BINARY_NAME)
	rm -rf dist/
	rm -f coverage.out coverage.html

# Run linter
lint:
	@echo "Running linter..."
	golangci-lint run

# Show help
help:
	@echo "Available targets:"
	@echo "  build         - Build the binary"
	@echo "  test          - Run tests"
	@echo "  test-coverage - Run tests with coverage"
	@echo "  clean         - Clean build artifacts"
	@echo "  lint          - Run linter"
	@echo "  help          - Show this help"
