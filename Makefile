.PHONY: all build run clean test

APP_NAME = go-llm-gateway
BUILD_DIR = build

all: clean build

build:
	@echo "Building $(APP_NAME)..."
	@go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/gateway/main.go

run: build
	@echo "Running $(APP_NAME)..."
	@./$(BUILD_DIR)/$(APP_NAME)

test:
	@echo "Running tests..."
	@go test -v -race -cover ./...

lint:
	@golangci-lint run ./...

clean:
	@rm -rf $(BUILD_DIR)