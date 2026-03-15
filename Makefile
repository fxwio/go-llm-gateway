SHELL := /bin/bash

APP_NAME := go-llm-gateway
BUILD_DIR := build
BINARY := $(BUILD_DIR)/$(APP_NAME)
VERSION ?= $(shell cat VERSION 2>/dev/null || echo dev)
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
IMAGE_REPOSITORY ?= ghcr.io/fxwio/go-llm-gateway
IMAGE_TAG ?= $(VERSION)
RELEASE_NAME ?= go-llm-gateway
NAMESPACE ?= go-llm-gateway-test
HELM_CHART ?= deploy/helm/go-llm-gateway
HELM_VALUES ?= $(HELM_CHART)/values-test.yaml
GO ?= go

GOFLAGS := -trimpath
LDFLAGS := -s -w \
	-X github.com/fxwio/go-llm-gateway/internal/buildinfo.Version=$(VERSION) \
	-X github.com/fxwio/go-llm-gateway/internal/buildinfo.Commit=$(GIT_COMMIT) \
	-X github.com/fxwio/go-llm-gateway/internal/buildinfo.BuildDate=$(BUILD_DATE)

.PHONY: all clean build test lint staticcheck security benchmark integration-test verify docker-build helm-lint deploy-test rollback-test changelog release-dry-run

all: verify build

clean:
	rm -rf $(BUILD_DIR)

build:
	mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/gateway/main.go

test:
	$(GO) test -race -cover ./...

lint:
	golangci-lint run ./...

staticcheck:
	staticcheck ./...

security:
	gosec ./...

benchmark:
	$(GO) test -run '^$$' -bench . -benchmem ./benchmark/...

integration-test:
	$(GO) test -tags=integration -count=1 ./integration/...

helm-lint:
	helm lint $(HELM_CHART) -f $(HELM_VALUES)

verify: lint staticcheck security test integration-test benchmark helm-lint

docker-build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(IMAGE_REPOSITORY):$(IMAGE_TAG) .

deploy-test:
	bash scripts/helm-upgrade.sh \
		$(RELEASE_NAME) \
		$(NAMESPACE) \
		$(HELM_CHART) \
		$(HELM_VALUES) \
		$(IMAGE_REPOSITORY) \
		$(IMAGE_TAG)

rollback-test:
	@test -n "$(REVISION)" || (echo "REVISION is required"; exit 1)
	helm rollback $(RELEASE_NAME) $(REVISION) --namespace $(NAMESPACE) --wait

changelog:
	bash scripts/release/generate-changelog.sh $(VERSION)

release-dry-run: verify docker-build changelog
