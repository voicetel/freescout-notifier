.PHONY: build clean test run-dev

BINARY_NAME=freescout-notifier
BUILD_DIR=build

build:
	go build -o $(BUILD_DIR)/$(BINARY_NAME) main.go

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./...

run-dev:
	go run ./cmd/notifier --dry-run --verbose

install-deps:
	go mod download
	go mod tidy

# Initialize the database
init-db:
	$(BUILD_DIR)/$(BINARY_NAME) --init-db

# Check connections
check-connections:
	$(BUILD_DIR)/$(BINARY_NAME) --check-connections

# Run with example flags
run-example:
	$(BUILD_DIR)/$(BINARY_NAME) \
		--freescout-user=readonly \
		--freescout-pass=password \
		--freescout-url=https://support.example.com \
		--slack-webhook=https://hooks.slack.com/services/YOUR/WEBHOOK/URL \
		--dry-run \
		--verbose
